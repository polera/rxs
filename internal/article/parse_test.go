package article

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/unicode"
)

func TestExtractCharsets(t *testing.T) {
	for _, tt := range []struct {
		name, header, meta, text, bom string
		encoding                      encoding.Encoding
	}{
		{name: "UTF8", header: "text/html; charset=UTF-8", text: "Caf\u00e9 mission"},
		{name: "UTF8 sniff", header: "text/html", text: "Caf\u00e9 mission"},
		{name: "UTF8 after ASCII prefix", header: "text/html", meta: strings.Repeat(" ", 2048), text: "Caf\u00e9 mission"},
		{name: "UTF8 BOM overrides header", header: "text/html; charset=windows-1252", text: "Caf\u00e9 mission", bom: "\xef\xbb\xbf"},
		{name: "UTF16LE BOM", header: "text/html; charset=windows-1252", text: "Caf\u00e9 mission", encoding: unicode.UTF16(unicode.LittleEndian, unicode.UseBOM)},
		{name: "UTF16BE BOM", header: "text/html; charset=utf-8", text: "Caf\u00e9 mission", encoding: unicode.UTF16(unicode.BigEndian, unicode.UseBOM)},
		{name: "header beats meta", header: `text/html; charset="windows-1252"; other=value`, meta: `<meta charset="utf-8">`, text: "Caf\u00e9 \u20ac mission", encoding: charmap.Windows1252},
		{name: "meta", header: "text/html", meta: `<meta charset="windows-1252">`, text: "Caf\u00e9 \u20ac mission", encoding: charmap.Windows1252},
		{name: "unknown header uses meta", header: "text/html; charset=unknown", meta: `<meta charset="shift_jis">`, text: "\u65e5\u672c\u8a9e mission", encoding: japanese.ShiftJIS},
		{name: "ShiftJIS", header: "text/html; charset=shift_jis", text: "\u65e5\u672c\u8a9e mission", encoding: japanese.ShiftJIS},
		{name: "default windows1252", header: "text/html", text: "Caf\u00e9 \u20ac mission", encoding: charmap.Windows1252},
		{name: "XHTML", header: "application/xhtml+xml; charset=windows-1252", text: "Caf\u00e9 mission", encoding: charmap.Windows1252},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := `<html><head>` + tt.meta + `<title>` + tt.text + `</title></head><body><nav>Account clutter</nav><article><h1>` + tt.text + `</h1><p>` + strings.Repeat(tt.text+" reports detailed engineering findings. ", 30) + `</p><p><a href="../related">Related mission</a></p></article></body></html>`
			if tt.encoding != nil {
				var err error
				body, err = tt.encoding.NewEncoder().String(body)
				if err != nil {
					t.Fatal(err)
				}
			}
			body = tt.bom + body
			extractor := &clientExtractor{http: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				result := response(request, http.StatusOK, tt.header, body)
				// Relative links and SourceURL must use the final response URL.
				final, _ := http.NewRequest(http.MethodGet, "https://public.example/final/story", nil)
				result.Request = final
				return result, nil
			})}}
			content, err := extractor.Extract(context.Background(), "https://public.example/old/story")
			if err != nil {
				t.Fatal(err)
			}
			if content.Title != tt.text || !strings.Contains(content.Text, tt.text) || strings.Contains(content.Text, "Account clutter") {
				t.Fatalf("bad extraction: %+v", content)
			}
			if content.SourceURL != "https://public.example/final/story" || !strings.Contains(content.HTML, `href="https://public.example/related"`) {
				t.Fatalf("bad resolved URL: %+v", content)
			}
		})
	}
}

func TestDecodeWholeBufferUTF8Fallback(t *testing.T) {
	for _, tt := range []struct {
		name, header, prefix, text, want string
	}{
		{"undeclared", "text/html", "", "Caf\u00e9", "Caf\u00e9"},
		{"unknown header", "text/html; charset=unknown", "", "Caf\u00e9", "Caf\u00e9"},
		{"unknown meta", "text/html", `<meta charset=unknown>`, "Caf\u00e9", "Caf\u00e9"},
		{"non-charset meta", "text/html", `<meta name=description content=article>`, "Caf\u00e9", "Caf\u00e9"},
		{"meta without pragma", "text/html", `<meta content="text/html; charset=windows-1252">`, "Caf\u00e9", "Caf\u00e9"},
		{"header wins over valid UTF8", "text/html; charset=windows-1252", "", "Caf\u00e9", "Caf\u00c3\u00a9"},
		{"meta wins over valid UTF8", "text/html", `<meta charset=windows-1252>`, "Caf\u00e9", "Caf\u00c3\u00a9"},
		{"pragma wins over valid UTF8", "text/html", `<meta http-equiv=content-type content="text/html; charset=windows-1252">`, "Caf\u00e9", "Caf\u00c3\u00a9"},
		{"quoted pragma charset", "text/html", `<META CONTENT="CHARSET = 'WINDOWS-1252'; other=value" HTTP-EQUIV=CONTENT-TYPE>`, "Caf\u00e9", "Caf\u00c3\u00a9"},
		{"pragma skips non-assignment", "text/html", `<meta http-equiv=content-type content="charset ignored; charset=windows-1252">`, "Caf\u00e9", "Caf\u00c3\u00a9"},
		{"unknown pragma charset", "text/html", `<meta http-equiv=content-type content="charset=unknown">`, "Caf\u00e9", "Caf\u00e9"},
		{"unterminated quoted charset", "text/html", `<meta http-equiv=content-type content="charset='windows-1252">`, "Caf\u00e9", "Caf\u00e9"},
		{"empty charset", "text/html", `<meta http-equiv=content-type content="charset=">`, "Caf\u00e9", "Caf\u00e9"},
		{"first duplicate charset wins", "text/html", `<meta charset=unknown charset=windows-1252>`, "Caf\u00e9", "Caf\u00e9"},
		{"first duplicate charset recognized", "text/html", `<meta charset=windows-1252 charset=unknown>`, "Caf\u00e9", "Caf\u00c3\u00a9"},
		{"unknown charset overrides content", "text/html", `<meta http-equiv=content-type content="charset=windows-1252" charset=unknown>`, "Caf\u00e9", "Caf\u00e9"},
		{"content after unknown charset", "text/html", `<meta charset=unknown content="charset=windows-1252" http-equiv=content-type>`, "Caf\u00e9", "Caf\u00c3\u00a9"},
		{"first duplicate pragma wins", "text/html", `<meta http-equiv=unknown http-equiv=content-type content="charset=windows-1252">`, "Caf\u00e9", "Caf\u00e9"},
		{"later recognized meta", "text/html", `<meta charset=unknown><meta charset=windows-1252>`, "Caf\u00e9", "Caf\u00c3\u00a9"},
		{"meta at sniff boundary", "text/html", strings.Repeat(" ", 997) + `<meta charset=windows-1252>`, "Caf\u00e9", "Caf\u00c3\u00a9"},
		{"meta truncated at sniff boundary", "text/html", strings.Repeat(" ", 998) + `<meta charset=windows-1252>`, "Caf\u00e9", "Caf\u00e9"},
		{"meta outside sniff window", "text/html", strings.Repeat(" ", 1024) + `<meta charset=windows-1252>`, "Caf\u00e9", "Caf\u00e9"},
		{"BOM wins over meta", "text/html; charset=windows-1252", "\xef\xbb\xbf<meta charset=windows-1252>", "Caf\u00e9", "Caf\u00e9"},
		{"late invalid UTF8 keeps legacy fallback", "text/html", "", "Caf\xe9", "Caf\u00e9"},
		{"partial UTF8 is not valid", "text/html", "", "Caf\xc3", "Caf\u00c3"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte(tt.prefix + strings.Repeat(" ", 2048) + tt.text)
			decoded, err := decodeArticle(context.Background(), body, tt.header)
			if err != nil || !strings.HasSuffix(string(decoded), tt.want) {
				t.Fatalf("decoded suffix = %q, error = %v; want %q", bytes.TrimSpace(decoded), err, tt.want)
			}
		})
	}
}

func TestDecodePreservesNormalizationPolicy(t *testing.T) {
	input := []byte("<p>Cafe\u0301 co\u00adoperate e\u00ad\u0301 &shy; &#173;</p>")
	got, err := decodeArticle(context.Background(), input, "text/html; charset=utf-8")
	if err != nil {
		t.Fatal(err)
	}
	if want := "<p>Caf\u00e9 cooperate \u00e9 &shy; &#173;</p>"; string(got) != want {
		t.Fatalf("decoded = %q, want %q", got, want)
	}
}

func TestDecodeLimits(t *testing.T) {
	for _, tt := range []struct {
		name, body, header string
		ok                 bool
	}{
		{"exact", strings.Repeat("x", int(maxDecodedBytes)), "text/html; charset=utf-8", true},
		{"over", strings.Repeat("x", int(maxDecodedBytes)+1), "text/html; charset=utf-8", false},
		{"fallback exact", strings.Repeat("x", int(maxDecodedBytes)-2) + "\u00e9", "text/html", true},
		{"fallback over", strings.Repeat("x", int(maxDecodedBytes)-1) + "\u00e9", "text/html", false},
		{"fallback normalization expansion", strings.Repeat(" ", 2048) + strings.Repeat("\u0344", 3<<19), "text/html", false},
		{"charset expansion", strings.Repeat("\x80", int(maxDecodedBytes)/3+1), "text/html; charset=windows-1252", false},
		{"removed expansion still capped", strings.Repeat("\xad", int(maxDecodedBytes)/2+1), "text/html; charset=windows-1252", false},
		{"normalization expansion", strings.Repeat("\u0344", 3<<19), "text/html; charset=utf-8", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			decoded, err := decodeArticle(context.Background(), []byte(tt.body), tt.header)
			if tt.ok {
				if err != nil || len(decoded) != int(maxDecodedBytes) {
					t.Fatalf("length = %d, error = %v", len(decoded), err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "decoded-input limit") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestPreflightTagBudget(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		ok         bool
	}{
		{"exact", strings.Repeat("<br>", maxDocumentTags), true},
		{"start tags", strings.Repeat("<br>", maxDocumentTags+1), false},
		{"closing tags", strings.Repeat("</p>", maxDocumentTags+1), false},
		{"self closing tags", strings.Repeat("<br/>", maxDocumentTags+1), false},
		{"foreign raw text", "<svg><title>" + strings.Repeat("<g/>", maxDocumentTags) + "</title></svg>", false},
		{"script conservative", "<script>" + strings.Repeat("<i>", maxDocumentTags) + "</script>", false},
		{"comment conservative", "<!--" + strings.Repeat("<i>", maxDocumentTags+1) + "-->", false},
		{"attribute conservative", `<p title="` + strings.Repeat("<", maxDocumentTags) + `">`, false},
		{"plaintext conservative", "<plaintext>" + strings.Repeat("<", maxDocumentTags), false},
		{"malformed", "<div><p x='unterminated", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := preflightArticle(context.Background(), []byte(tt.body))
			if tt.ok && err != nil || !tt.ok && (err == nil || !strings.Contains(err.Error(), "tag preflight limit")) {
				t.Fatalf("error = %v, want accepted %v", err, tt.ok)
			}
		})
	}
}

func TestPreflightRejectsScriptStateBypasses(t *testing.T) {
	for _, prefix := range []string{
		"<script><!--</script>",
		`<script>"<p title='"</script>`,
		`<script>'<p title="'</script>`,
	} {
		t.Run(prefix, func(t *testing.T) {
			// A small DOM verifies that these really are nodes, not script text,
			// without allocating the attack's million-node DOM in this test.
			doc, err := html.Parse(strings.NewReader(prefix + strings.Repeat("<br>", 10)))
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for node := range doc.Descendants() {
				if node.Type == html.ElementNode && node.Data == "br" {
					count++
				}
			}
			if count != 10 {
				t.Fatalf("fixture produced %d br elements, want 10", count)
			}
			body := []byte(prefix + strings.Repeat("<br>", 1_000_000))
			if len(body) > int(maxResponseBytes) {
				t.Fatal("fixture exceeds response cap")
			}
			if err := preflightArticle(context.Background(), body); err == nil || !strings.Contains(err.Error(), "tag preflight limit") {
				t.Fatalf("preflight accepted a million-node input: %v", err)
			}
			pageURL, _ := url.Parse("https://public.example/article")
			if _, err := extractArticle(context.Background(), body, "text/html", pageURL); err == nil || !strings.Contains(err.Error(), "tag preflight limit") {
				t.Fatalf("extraction did not reject before DOM construction: %v", err)
			}
		})
	}
}

func TestExtractRetainsDOMElementLimit(t *testing.T) {
	pageURL, _ := url.Parse("https://public.example/article")
	// Preflight allows exactly this many tags, but the tree builder adds the
	// implied html/head/body elements. Readability must still enforce its limit.
	_, err := extractArticle(context.Background(), []byte(strings.Repeat("<br>", maxDocumentTags)), "text/html", pageURL)
	if err == nil || !strings.Contains(err.Error(), "documents too large") {
		t.Fatalf("error = %v", err)
	}
}

// Cancellation is triggered by a check, not elapsed time, so every cooperative
// checkpoint can be exercised deterministically, including after CPU-only stages.
type checkpointContext struct {
	context.Context
	remaining int
	done      chan struct{}
}

func (c *checkpointContext) Done() <-chan struct{} { return c.done }

func (c *checkpointContext) Err() error {
	if c.remaining > 0 {
		c.remaining--
		return nil
	}
	select {
	case <-c.done:
	default:
		close(c.done)
	}
	return context.Canceled
}

func TestExtractCancellationAtEveryCheckpoint(t *testing.T) {
	pageURL, _ := url.Parse("https://public.example/article")
	body := []byte(benchmarkPage(2048))
	for checkpoint := 0; checkpoint < 1000; checkpoint++ {
		ctx := &checkpointContext{Context: context.Background(), remaining: checkpoint, done: make(chan struct{})}
		content, err := extractArticle(ctx, body, "text/html; charset=utf-8", pageURL)
		if err == nil {
			if !strings.Contains(content.Text, "mission engineers") || checkpoint < 10 {
				t.Fatalf("unexpected success at checkpoint %d: %+v", checkpoint, content)
			}
			t.Logf("checked %d cancellation checkpoints", checkpoint)
			return
		}
		if !errors.Is(err, context.Canceled) || content != (Content{}) {
			t.Fatalf("checkpoint %d: content = %+v, error = %v", checkpoint, content, err)
		}
	}
	t.Fatal("extraction never completed")
}

type trackedBody struct {
	io.Reader
	closed bool
}

func (b *trackedBody) Close() error { b.closed = true; return nil }

type readerFunc func([]byte) (int, error)

func (r readerFunc) Read(p []byte) (int, error) { return r(p) }

func TestExtractBodyErrorsAndClosure(t *testing.T) {
	sentinel := errors.New("body read failed")
	for _, mode := range []string{"read error", "cancel", "response limit", "decoded limit", "tag limit", "success"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var source io.Reader = strings.NewReader(benchmarkPage(2048))
			header := "text/html; charset=utf-8"
			switch mode {
			case "read error":
				source = readerFunc(func([]byte) (int, error) { return 0, sentinel })
			case "cancel":
				source = readerFunc(func(p []byte) (int, error) { cancel(); return copy(p, "<p>article</p>"), io.EOF })
			case "response limit":
				source = strings.NewReader(strings.Repeat("x", int(maxResponseBytes)+1))
			case "decoded limit":
				header = "text/html; charset=windows-1252"
				source = strings.NewReader(strings.Repeat("\x80", int(maxDecodedBytes)/3+1))
			case "tag limit":
				source = strings.NewReader(strings.Repeat("<br>", maxDocumentTags+1))
			}
			body := &trackedBody{Reader: source}
			extractor := &clientExtractor{http: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				result := response(request, http.StatusOK, header, "")
				result.Body = body
				return result, nil
			})}}
			_, err := extractor.Extract(ctx, "https://public.example/article")
			if !body.closed || (mode == "success") != (err == nil) {
				t.Fatalf("closed = %v, error = %v", body.closed, err)
			}
			if mode == "read error" && !errors.Is(err, sentinel) || mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func FuzzArticlePreflight(f *testing.F) {
	f.Add([]byte("<p>Cafe\u0301 co\u00adoperate &shy;</p>"), "text/html; charset=utf-8")
	f.Add([]byte("\xff\xfe<\x00p\x00>\x00"), "text/html")
	f.Add([]byte("<meta charset=windows-1252><p>\x80\xe9</p>"), "text/html")
	f.Add([]byte("<svg><title><g/></title></svg><script><i></script>"), "text/html")
	f.Add([]byte("<script><!--</script><br>"), "text/html")
	f.Add([]byte(`<script>"<p title='"</script><br>`), "text/html")
	f.Add([]byte(strings.Repeat("<", maxDocumentTags+1)), "text/html; charset=utf-8")
	f.Add([]byte(strings.Repeat(" ", 2048)+"Caf\u00e9"), "text/html")
	f.Add([]byte(`<meta http-equiv=content-type content="charset='windows-1252'">`+strings.Repeat(" ", 2048)+"Caf\u00e9"), "text/html")
	f.Fuzz(func(t *testing.T, body []byte, header string) {
		if len(body) > 100<<10 || len(header) > 1024 {
			t.Skip()
		}
		decoded, err := decodeArticle(context.Background(), body, header)
		if err != nil {
			return
		}
		if len(decoded) > int(maxDecodedBytes) || !utf8.Valid(decoded) || bytes.Contains(decoded, []byte("\u00ad")) {
			t.Fatal("decoded-input invariant failed")
		}
		err = preflightArticle(context.Background(), decoded)
		if over := bytes.Count(decoded, []byte{'<'}) > maxDocumentTags; (err != nil) != over {
			t.Fatalf("candidate budget: over = %v, error = %v", over, err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := preflightArticle(ctx, decoded); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled preflight: %v", err)
		}
	})
}
