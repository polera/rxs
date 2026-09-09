package article

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type resolverFunc func(context.Context, string, string) ([]net.IP, error)

func (r resolverFunc) LookupIP(ctx context.Context, network, host string) ([]net.IP, error) {
	return r(ctx, network, host)
}

func TestSafeDialerStalledAddressFallsBack(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	parentDeadline, _ := ctx.Deadline()
	connection, peer := net.Pipe()
	defer peer.Close()
	defer connection.Close()
	calls := 0
	var attempts []context.Context
	dialer := safeDialer{
		resolver: resolverStub{ips: map[string][]net.IP{"public.example": {net.ParseIP("93.184.216.34"), net.ParseIP("2606:4700:4700::1111")}}},
		dial: func(attempt context.Context, network, address string) (net.Conn, error) {
			calls++
			attempts = append(attempts, attempt)
			if network != "tcp" {
				t.Fatalf("network = %q", network)
			}
			deadline, ok := attempt.Deadline()
			if !ok || deadline.After(parentDeadline) {
				t.Fatalf("invalid attempt deadline: %v", deadline)
			}
			if calls == 1 {
				if address != "93.184.216.34:443" || !deadline.Before(parentDeadline) {
					t.Fatalf("first attempt: %s, deadline %v", address, deadline)
				}
				<-attempt.Done()
				return nil, attempt.Err()
			}
			if address != "[2606:4700:4700::1111]:443" || ctx.Err() != nil || attempt.Err() != nil {
				t.Fatalf("fallback has no budget or wrong address: %s, %v", address, attempt.Err())
			}
			return connection, nil
		},
	}
	got, err := dialer.DialContext(ctx, "tcp", "public.example:443")
	if err != nil || got != connection || calls != 2 {
		t.Fatalf("connection = %v, error = %v, calls = %d", got, err, calls)
	}
	for _, attempt := range attempts {
		select {
		case <-attempt.Done():
		default:
			t.Fatal("attempt context was not canceled promptly")
		}
	}
}

func TestSafeDialerDividesRemainingBudgetAndCancelsAttempts(t *testing.T) {
	firstError, lastError := errors.New("first failure"), errors.New("last failure")
	for _, withDeadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "no parent deadline", true: "parent deadline"}[withDeadline], func(t *testing.T) {
			ctx := context.Background()
			if withDeadline {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 3*time.Second)
				defer cancel()
			}
			start := time.Now()
			var previous context.Context
			calls := 0
			dialer := safeDialer{
				resolver: resolverFunc(func(resolveCtx context.Context, _, _ string) ([]net.IP, error) {
					deadline, ok := resolveCtx.Deadline()
					if !ok || deadline.After(time.Now().Add(requestTimeout)) {
						t.Fatal("DNS has no bounded deadline")
					}
					return []net.IP{net.ParseIP("1.1.1.1"), net.ParseIP("8.8.8.8"), net.ParseIP("9.9.9.9")}, nil
				}),
				dial: func(attempt context.Context, _, _ string) (net.Conn, error) {
					if previous != nil && !errors.Is(previous.Err(), context.Canceled) {
						t.Fatal("failed attempt not canceled before fallback")
					}
					deadline, ok := attempt.Deadline()
					totalDeadline := start.Add(requestTimeout)
					if parentDeadline, exists := ctx.Deadline(); exists {
						totalDeadline = parentDeadline
					}
					remaining := 3 - calls
					now := time.Now()
					// Bound the expected deadline using the start and current time,
					// allowing only timer creation overhead, not a whole extra share.
					lower := start.Add(totalDeadline.Sub(start) / time.Duration(remaining))
					upper := now.Add(totalDeadline.Sub(now)/time.Duration(remaining) + time.Millisecond)
					if !ok || deadline.Before(lower.Add(-time.Millisecond)) || deadline.After(upper) {
						t.Fatalf("attempt %d deadline %v outside [%v, %v]", calls, deadline, lower, upper)
					}
					previous = attempt
					calls++
					if calls == 1 {
						return nil, firstError
					}
					return nil, lastError
				},
			}
			_, err := dialer.DialContext(ctx, "tcp", "public.example:80")
			if calls != 3 || !errors.Is(err, firstError) || !errors.Is(err, lastError) || previous.Err() != context.Canceled {
				t.Fatalf("calls = %d, error = %v", calls, err)
			}
		})
	}
}

func TestSafeDialerParentCancellation(t *testing.T) {
	for _, stage := range []string{"before DNS", "during DNS", "during dial", "dial succeeds after cancellation"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if stage == "before DNS" {
				cancel()
			}
			connection, peer := net.Pipe()
			defer connection.Close()
			defer peer.Close()
			calls, lookups := 0, 0
			dialer := safeDialer{
				resolver: resolverFunc(func(ctx context.Context, _, _ string) ([]net.IP, error) {
					lookups++
					if stage == "during DNS" {
						cancel()
						return nil, ctx.Err()
					}
					return []net.IP{net.ParseIP("1.1.1.1"), net.ParseIP("8.8.8.8")}, nil
				}),
				dial: func(attempt context.Context, _, _ string) (net.Conn, error) {
					calls++
					cancel()
					if stage == "dial succeeds after cancellation" {
						return connection, nil
					}
					<-attempt.Done()
					return nil, attempt.Err()
				},
			}
			got, err := dialer.DialContext(ctx, "tcp", "public.example:443")
			if got != nil || !errors.Is(err, context.Canceled) || calls > 1 || stage == "before DNS" && lookups != 0 {
				t.Fatalf("connection = %v, error = %v, calls = %d, lookups = %d", got, err, calls, lookups)
			}
			if stage == "dial succeeds after cancellation" {
				_ = peer.SetWriteDeadline(time.Now())
				if _, err := peer.Write([]byte("x")); !errors.Is(err, io.ErrClosedPipe) {
					t.Fatal("connection returned after cancellation was not closed")
				}
			}
		})
	}
}

func TestSafeDialerRejectsUnsafeAnswersBeforeAnyAttempt(t *testing.T) {
	for _, ips := range [][]net.IP{
		{net.ParseIP("1.1.1.1"), net.ParseIP("10.0.0.1")},
		{net.ParseIP("127.0.0.1")},
		{net.ParseIP("::ffff:169.254.169.254")},
		{net.ParseIP("2001:db8::1")},
		{nil},
		nil,
	} {
		dialer := safeDialer{
			resolver: resolverStub{ips: map[string][]net.IP{"public.example": ips}},
			dial: func(context.Context, string, string) (net.Conn, error) {
				t.Fatal("attempted to dial an unsafe answer set")
				return nil, nil
			},
		}
		if _, err := dialer.DialContext(context.Background(), "tcp", "public.example:80"); err == nil {
			t.Fatalf("accepted unsafe answers: %v", ips)
		}
	}
}

func TestSafeDialerRevalidatesDNSAndDialsOnlyLiterals(t *testing.T) {
	for _, rebindPrivate := range []bool{false, true} {
		lookups, calls := 0, 0
		resolver := resolverFunc(func(_ context.Context, network, host string) ([]net.IP, error) {
			lookups++
			if network != "ip" || host != "public.example" {
				t.Fatalf("lookup = %q %q", network, host)
			}
			if lookups > 1 && rebindPrivate {
				return []net.IP{net.ParseIP("127.0.0.1")}, nil
			}
			return []net.IP{net.ParseIP("1.1.1.1")}, nil
		})
		target, _ := url.Parse("https://public.example/article")
		if err := (destinationValidator{resolver: resolver}).Validate(context.Background(), target); err != nil {
			t.Fatal(err)
		}
		sentinel := errors.New("dial failure")
		dialer := safeDialer{resolver: resolver, dial: func(_ context.Context, _, address string) (net.Conn, error) {
			calls++
			if address != "1.1.1.1:443" {
				t.Fatalf("dialing unvalidated address: %s", address)
			}
			return nil, sentinel
		}}
		_, err := dialer.DialContext(context.Background(), "tcp", "public.example:443")
		if lookups != 2 || rebindPrivate && (calls != 0 || err == nil || !strings.Contains(err.Error(), "not public")) || !rebindPrivate && (calls != 1 || !errors.Is(err, sentinel)) {
			t.Fatalf("private rebinding = %v, lookups = %d, calls = %d, error = %v", rebindPrivate, lookups, calls, err)
		}
		_, _ = dialer.DialContext(context.Background(), "tcp", "1.1.1.1:443")
		if lookups != 2 {
			t.Fatal("literal address triggered DNS lookup")
		}
	}
}

func TestConfiguredTransportDisablesProxy(t *testing.T) {
	transport := NewExtractor("test").(*clientExtractor).http.Transport.(*http.Transport)
	if transport.Proxy != nil || transport.DialContext == nil {
		t.Fatal("configured transport must use safe dialing without proxies")
	}
}
