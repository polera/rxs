package upgrade

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type trackedHTTPBody struct {
	io.Reader
	read, closed int
}

func (b *trackedHTTPBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.read += n
	return n, err
}

func (b *trackedHTTPBody) Close() error {
	b.closed++
	return nil
}

type httpErrorReader struct{ err error }

func (r httpErrorReader) Read([]byte) (int, error) { return 0, r.err }

func TestBoundedHTTP(t *testing.T) {
	sentinel := errors.New("reader failed")
	const releaseJSON = `{"tag_name":"v1.2.0","assets":[{"name":"rxs_linux_arm64.tar.gz","browser_download_url":"https://release.test/archive"},{"name":"checksums.txt","browser_download_url":"https://release.test/checksums"}]}`
	for _, latest := range []bool{false, true} {
		name, limit, content := "download", 32, "binary"
		requestPrefix, readPrefix, sizeMessage := "", "", "download is too large"
		if latest {
			name, limit, content = "latest", maxAPIResponse, releaseJSON
			requestPrefix, readPrefix, sizeMessage = "check GitHub releases: ", "read GitHub release: ", "GitHub release response is too large"
		}
		t.Run(name, func(t *testing.T) {
			for _, test := range []struct {
				name    string
				size    int
				status  int
				readErr error
				wantErr string
			}{
				{"short", len(content), http.StatusOK, nil, ""},
				{"exact", limit, http.StatusOK, nil, ""},
				{"one over", limit + 1, http.StatusOK, nil, sizeMessage},
				{"well over", limit + 100, http.StatusOK, nil, sizeMessage},
				{"read error", len(content), http.StatusOK, sentinel, readPrefix + sentinel.Error()},
				{"error at limit", limit, http.StatusOK, sentinel, readPrefix + sentinel.Error()},
				{"status", len(content), http.StatusServiceUnavailable, nil, requestPrefix + "HTTP 503 Service Unavailable"},
			} {
				t.Run(test.name, func(t *testing.T) {
					data := content + strings.Repeat(" ", test.size-len(content))
					var reader io.Reader = strings.NewReader(data)
					if test.readErr != nil {
						reader = io.MultiReader(reader, httpErrorReader{test.readErr})
					}
					body := &trackedHTTPBody{Reader: reader}
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					client := &Client{LatestURL: "https://api.test/latest", GOOS: "linux", GOARCH: "arm64"}
					client.HTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
						wantAccept := ""
						if latest {
							wantAccept = "application/vnd.github+json"
						}
						if req.Method != http.MethodGet || req.Header.Get("User-Agent") != "rxs-updater" || req.Header.Get("Accept") != wantAccept || req.Context() != ctx {
							t.Errorf("unexpected request: %s, %v, context matches: %v", req.Method, req.Header, req.Context() == ctx)
						}
						return &http.Response{StatusCode: test.status, Status: "503 Service Unavailable", Body: body, Header: make(http.Header), Request: req}, nil
					})}
					var err error
					if latest {
						_, err = client.Latest(ctx)
					} else {
						var got []byte
						got, err = client.download(ctx, "https://release.test/archive", int64(limit))
						if test.wantErr == "" && string(got) != data {
							t.Fatalf("download returned %d bytes, want %d", len(got), len(data))
						}
					}
					if test.wantErr == "" && err != nil || test.wantErr != "" && (err == nil || err.Error() != test.wantErr) {
						t.Fatalf("error = %v, want %q", err, test.wantErr)
					}
					if test.readErr != nil && !errors.Is(err, test.readErr) {
						t.Fatalf("lost reader error: %v", err)
					}
					wantRead := min(test.size, limit+1)
					if test.status != http.StatusOK {
						wantRead = 0
					}
					if body.closed != 1 || body.read != wantRead {
						t.Fatalf("body closed %d times, read %d bytes; want 1, %d", body.closed, body.read, wantRead)
					}
				})
			}
		})
	}
}

func TestHTTPTransportErrors(t *testing.T) {
	for _, latest := range []bool{false, true} {
		for _, cause := range []error{errors.New("transport failed"), context.Canceled, context.DeadlineExceeded} {
			client := &Client{HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return nil, cause
			})}}
			var err error
			if latest {
				_, err = client.Latest(context.Background())
				if err == nil || !strings.HasPrefix(err.Error(), "check GitHub releases: ") {
					t.Fatalf("Latest error = %v", err)
				}
			} else {
				_, err = client.download(context.Background(), "https://release.test/archive", 32)
			}
			if !errors.Is(err, cause) {
				t.Fatalf("error = %v, want cause %v", err, cause)
			}
		}
	}
}

func TestHTTPBodyCancellation(t *testing.T) {
	for _, latest := range []bool{false, true} {
		t.Run(map[bool]string{false: "download", true: "latest"}[latest], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				<-req.Context().Done()
			}))
			defer server.Close()
			var body *trackedHTTPBody
			transport := server.Client().Transport
			client := &Client{LatestURL: server.URL, HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				response, err := transport.RoundTrip(req)
				if err == nil {
					body = &trackedHTTPBody{Reader: response.Body}
					// Preserve the real body's Close as well as tracking ownership.
					response.Body = &trackedNetworkBody{trackedHTTPBody: body, closer: response.Body}
					cancel()
				}
				return response, err
			})}}
			var err error
			if latest {
				_, err = client.Latest(ctx)
			} else {
				_, err = client.download(ctx, server.URL, 32)
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v", err)
			}
			if body == nil || body.closed != 1 {
				t.Fatalf("body = %+v, want a body closed exactly once", body)
			}
		})
	}
}

type trackedNetworkBody struct {
	*trackedHTTPBody
	closer io.Closer
}

func (b *trackedNetworkBody) Close() error {
	b.closed++
	return b.closer.Close()
}

func TestHTTPClientTimeoutPolicy(t *testing.T) {
	if got := NewClient().httpClient().Timeout; got != 15*time.Second {
		t.Fatalf("constructor timeout = %v", got)
	}
	client := &Client{}
	if got := client.httpClient(); got == http.DefaultClient || got.Timeout != NewClient().HTTPClient.Timeout {
		t.Fatalf("fallback client = %+v", got)
	}
	custom := &http.Client{Timeout: time.Minute}
	client.HTTPClient = custom
	if client.httpClient() != custom {
		t.Fatal("custom HTTP client replaced")
	}
}
