package article

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	readability "codeberg.org/readeck/go-readability/v2"
	"golang.org/x/net/html"
)

func benchmarkPage(size int) string {
	prefix := `<!doctype html><html><head><title>Mission Engineering Report</title></head><body><nav>Home Account</nav><article><h1>Mission Engineering Report</h1>`
	suffix := `</article><aside>Advertisement</aside></body></html>`
	paragraph := `<p>` + strings.Repeat("The mission engineers investigated propulsion, navigation, and communications systems. ", 20) + `<a href="/report">Full report</a></p>`
	var body strings.Builder
	body.Grow(size)
	body.WriteString(prefix)
	for body.Len()+len(paragraph)+len(suffix) <= size {
		body.WriteString(paragraph)
	}
	body.WriteString(strings.Repeat(" ", size-body.Len()-len(suffix)))
	body.WriteString(suffix)
	return body.String()
}

// Fixtures and the in-memory transport are outside the timed region. This covers
// the complete extraction pipeline, including bounded reads and final rendering.
func BenchmarkExtract(b *testing.B) {
	for _, fixture := range []struct {
		name string
		body string
	}{
		{"100KiB", benchmarkPage(100 << 10)},
		{"1MiB", benchmarkPage(1 << 20)},
		{"5MiB", benchmarkPage(5 << 20)},
		{"dense", strings.Repeat("<i></i>", int(maxResponseBytes)/7)},
	} {
		b.Run(fixture.name, func(b *testing.B) {
			extractor := &clientExtractor{http: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return response(request, http.StatusOK, "text/html; charset=utf-8", fixture.body), nil
			})}}
			b.ReportAllocs()
			b.SetBytes(int64(len(fixture.body)))
			b.ResetTimer()
			for b.Loop() {
				content, err := extractor.Extract(context.Background(), "https://public.example/article")
				if fixture.name == "dense" {
					if err == nil {
						b.Fatal("dense input accepted")
					}
				} else if err != nil || !strings.Contains(content.Text, "mission engineers") {
					b.Fatalf("extraction failed: %v", err)
				}
			}
		})
	}
}

// Compare parsing paths in the same binary, independently of HTTP reads and
// internal/render changes. Legacy retains the old decoder and post-DOM limit.
func BenchmarkArticleParse(b *testing.B) {
	pageURL, _ := url.Parse("https://public.example/article")
	for _, fixture := range []struct {
		name string
		body string
	}{
		{"100KiB", benchmarkPage(100 << 10)},
		{"1MiB", benchmarkPage(1 << 20)},
		{"5MiB", benchmarkPage(5 << 20)},
		{"dense", strings.Repeat("<i></i>", int(maxResponseBytes)/7)},
	} {
		body := []byte(fixture.body)
		for _, legacy := range []bool{true, false} {
			mode := "bounded"
			if legacy {
				mode = "legacy"
			}
			b.Run(fixture.name+"/"+mode, func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(body)))
				for b.Loop() {
					parser := readability.NewParser()
					parser.MaxElemsToParse = maxDocumentElements
					var extracted readability.Article
					var err error
					if legacy {
						extracted, err = parser.Parse(bytes.NewReader(body), pageURL)
					} else {
						ctx := context.Background()
						var decoded []byte
						decoded, err = decodeArticle(ctx, body, "text/html; charset=utf-8")
						if err == nil {
							err = preflightArticle(ctx, decoded)
						}
						if err == nil {
							var doc *html.Node
							doc, err = html.Parse(contextReader{ctx, bytes.NewReader(decoded)})
							if err == nil {
								extracted, err = parser.ParseAndMutate(doc, pageURL)
							}
						}
					}
					if fixture.name == "dense" {
						if err == nil {
							b.Fatal("dense input accepted")
						}
					} else if err != nil || extracted.Node == nil {
						b.Fatalf("parsing failed: %v", err)
					}
				}
			})
		}
	}
}

func BenchmarkArticleDecode(b *testing.B) {
	for _, fixture := range []struct {
		name string
		size int
	}{{"100KiB", 100 << 10}, {"1MiB", 1 << 20}, {"5MiB", 5 << 20}} {
		text := "<p>Caf\u00e9</p>"
		body := []byte(strings.Repeat(" ", fixture.size-len(text)) + text)
		for _, header := range []string{"text/html; charset=utf-8", "text/html"} {
			b.Run(fixture.name+"/"+header, func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(body)))
				for b.Loop() {
					decoded, err := decodeArticle(context.Background(), body, header)
					if err != nil || !bytes.HasSuffix(decoded, []byte(text)) {
						b.Fatalf("decode failed: %v", err)
					}
				}
			})
		}
	}
}
