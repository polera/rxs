package article

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func response(request *http.Request, status int, contentType, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
}

func TestExtractRemovesClutterAndResolvesRelativeLinks(t *testing.T) {
	body := strings.Repeat("The launch article contains detailed reporting about the mission and its engineering systems. ", 12)
	html := `<!doctype html><html><head><title>Mission Launch Details</title></head><body>
<nav>Home Products Account Sign in</nav><article><h1>Mission Launch Details</h1><p>` + body +
		`</p><p><a href="/related">Related mission</a></p></article><aside>Advertisement</aside></body></html>`
	var agent string
	extractor := &clientExtractor{userAgent: "rxs/test (+https://github.com/polera/rxs)", http: &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			agent = request.Header.Get("User-Agent")
			return response(request, http.StatusOK, "text/html; charset=utf-8", html), nil
		}),
	}}
	content, err := extractor.Extract(context.Background(), "https://public.example/articles/launch")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content.Text, "detailed reporting") || strings.Contains(content.Text, "Products Account") {
		t.Fatalf("extracted text = %q", content.Text)
	}
	if !strings.Contains(content.HTML, `href="https://public.example/related"`) {
		t.Fatalf("relative link was not resolved: %s", content.HTML)
	}
	if agent != "rxs/test (+https://github.com/polera/rxs)" {
		t.Fatalf("user agent = %q", agent)
	}
}

func TestExtractRejectsBadResponses(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		contentType string
		body        string
		contains    string
	}{
		{name: "HTTP error", status: http.StatusUnauthorized, contentType: "text/html", body: "denied", contains: "401"},
		{name: "non HTML", status: http.StatusOK, contentType: "application/pdf", body: "%PDF", contains: "not HTML"},
		{name: "oversized", status: http.StatusOK, contentType: "text/html", body: strings.Repeat("x", int(maxResponseBytes)+1), contains: "exceeds 5 MiB"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			extractor := &clientExtractor{http: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return response(request, test.status, test.contentType, test.body), nil
			})}}
			_, err := extractor.Extract(context.Background(), "https://public.example/article")
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("error = %v, want containing %q", err, test.contains)
			}
		})
	}
}

func TestExtractHonorsHTTPClientTimeout(t *testing.T) {
	extractor := &clientExtractor{http: &http.Client{
		Timeout: 5 * time.Millisecond,
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			<-request.Context().Done()
			return nil, request.Context().Err()
		}),
	}}
	_, err := extractor.Extract(context.Background(), "https://public.example/article")
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("timeout error = %v", err)
	}
}

func TestHTTPClientStopsAfterFiveRedirects(t *testing.T) {
	requests := 0
	client := &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			requests++
			result := response(request, http.StatusFound, "text/html", "")
			result.Header.Set("Location", "/again")
			return result, nil
		}),
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) > 5 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
	extractor := &clientExtractor{http: client}
	_, err := extractor.Extract(context.Background(), "https://public.example/article")
	if err == nil || requests != 6 {
		t.Fatalf("requests = %d, error = %v; want 6 requests and an HTTP error", requests, err)
	}
}

func TestConfiguredClientRejectsPrivateRedirectAndRedirectLoop(t *testing.T) {
	extractor := NewExtractor("test").(*clientExtractor)
	initial, _ := http.NewRequest(http.MethodGet, "https://public.example/article", nil)
	private, _ := http.NewRequest(http.MethodGet, "http://169.254.169.254/latest/meta-data", nil)
	if err := extractor.http.CheckRedirect(private, []*http.Request{initial}); err == nil || !strings.Contains(err.Error(), "not public") {
		t.Fatalf("private redirect error = %v", err)
	}
	public, _ := http.NewRequest(http.MethodGet, "https://public.example/again", nil)
	via := []*http.Request{initial, initial, initial, initial, initial, initial}
	if err := extractor.http.CheckRedirect(public, via); err == nil || !strings.Contains(err.Error(), "5 redirects") {
		t.Fatalf("redirect limit error = %v", err)
	}
}

type resolverStub struct {
	ips map[string][]net.IP
}

func (r resolverStub) LookupIP(_ context.Context, _, host string) ([]net.IP, error) {
	return r.ips[host], nil
}

func TestDestinationValidatorRejectsNonPublicTargets(t *testing.T) {
	resolver := resolverStub{ips: map[string][]net.IP{
		"private.example": {net.ParseIP("10.0.0.1")},
		"mixed.example":   {net.ParseIP("93.184.216.34"), net.ParseIP("169.254.169.254")},
		"public.example":  {net.ParseIP("93.184.216.34")},
	}}
	validator := destinationValidator{resolver: resolver}
	for _, rawURL := range []string{
		"http://127.0.0.1/article",
		"http://[::1]/article",
		"http://169.254.169.254/latest/meta-data",
		"http://100.100.100.200/latest/meta-data",
		"http://private.example/article",
		"http://mixed.example/article",
		"http://metadata.google.internal/computeMetadata/v1/",
		"http://user:pass@public.example/article",
	} {
		target, _ := url.Parse(rawURL)
		if err := validator.Validate(context.Background(), target); err == nil {
			t.Fatalf("destination %q was accepted", rawURL)
		}
	}
	public, _ := url.Parse("https://public.example/article")
	if err := validator.Validate(context.Background(), public); err != nil {
		t.Fatalf("public destination rejected: %v", err)
	}
}
