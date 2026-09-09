package opml

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"
	"unicode/utf8"

	"github.com/polera/rxs/internal/domain"
)

func TestImportNestedAndExport(t *testing.T) {
	input := `<opml version="2.0"><body><outline text="Folder"><outline text="Example" xmlUrl="https://example.com/feed" htmlUrl="https://example.com"/></outline></body></opml>`
	subscriptions, err := Import(strings.NewReader(input))
	if err != nil || len(subscriptions) != 1 || subscriptions[0].Title != "Example" {
		t.Fatalf("Import() = %#v, %v", subscriptions, err)
	}
	var output bytes.Buffer
	err = Export(&output, []domain.Feed{{Title: subscriptions[0].Title, URL: subscriptions[0].FeedURL, SiteURL: subscriptions[0].SiteURL}})
	if err != nil || !strings.Contains(output.String(), `xmlUrl="https://example.com/feed"`) {
		t.Fatalf("Export() = %q, %v", output.String(), err)
	}
}

func TestImportCompleteDocument(t *testing.T) {
	const root = `<opml><body><outline title=" Title " text="Fallback" xmlUrl=" https://example.com/feed " htmlUrl=" https://example.com "/></body></opml>`
	for _, test := range []struct {
		name, suffix string
		valid        bool
	}{
		{"root only", "", true},
		{"XML whitespace", " \t\r\n", true},
		{"comments", " <!-- first -->\n<!-- second -->", true},
		{"processing instructions", "<?style test?> <!-- comment --> <?other?>\r\n", true},
		{"second root", "<opml/>", false},
		{"different root", "<other/>", false},
		{"text", "junk", false},
		{"non XML whitespace", "\u00a0", false},
		{"character reference", "&#32;", false},
		{"CDATA", "<![CDATA[ ]]>", false},
		{"empty CDATA", "<![CDATA[]]>", false},
		{"XML declaration", `<?xml version="1.0"?>`, false},
		{"reserved PI", "<?XML test?>", false},
		{"directive", "<!DOCTYPE opml>", false},
		{"truncated tag", "<", false},
		{"truncated comment", "<!--", false},
		{"bad comment", "<!-- a -- b -->", false},
		{"truncated PI", "<?target", false},
		{"bad closing tag", "</opml>", false},
		{"invalid UTF8", "\xff", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, reader := range []io.Reader{strings.NewReader(root + test.suffix), iotest.OneByteReader(strings.NewReader(root + test.suffix))} {
				got, err := Import(reader)
				if !test.valid {
					if err == nil || got != nil {
						t.Fatalf("Import() = %+v, %v; want no subscriptions and error", got, err)
					}
					continue
				}
				want := []Subscription{{Title: "Title", FeedURL: "https://example.com/feed", SiteURL: "https://example.com"}}
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("Import() = %+v, %v; want %+v", got, err, want)
				}
			}
		})
	}
}

func TestImportProlog(t *testing.T) {
	for _, prefix := range []string{"", " \r\n\t", xml.Header, "<!--comment-->", "<?style test?>", "<!DOCTYPE opml>", xml.Header + "<!DOCTYPE opml>\n<!--comment-->"} {
		if _, err := Import(strings.NewReader(prefix + "<opml/>")); err != nil {
			t.Errorf("prefix %q: %v", prefix, err)
		}
	}
	for _, input := range []string{"", "<!--comment-->", "<other/>", "junk<opml/>", "&#32;<opml/>", "<![CDATA[ ]]><opml/>", " " + xml.Header + "<opml/>", xml.Header + xml.Header + "<opml/>", "<?XML?><opml/>", "<!other><opml/>", "<!DOCTYPE opml><!DOCTYPE opml><opml/>"} {
		if got, err := Import(strings.NewReader(input)); err == nil || got != nil {
			t.Errorf("input %q: Import() = %+v, %v", input, got, err)
		}
	}
}

func TestImportBOM(t *testing.T) {
	for _, test := range []struct {
		input string
		valid bool
	}{
		{"\xef\xbb\xbf<opml/>", true},
		{"\xef\xbb\xbf" + xml.Header + "<opml/>", true},
		{"\xef\xbb\xbf \r\n<!--comment--><opml/>", true},
		{"\xef\xbb\xbf " + xml.Header + "<opml/>", false},
		{"\xef\xbb\xbf\xef\xbb\xbf<opml/>", false},
		{" \xef\xbb\xbf<opml/>", false},
		{xml.Header + "\xef\xbb\xbf<opml/>", false},
		{"<opml/>\xef\xbb\xbf", false},
		{"\xef\xbb<opml/>", false},
		{"\xef\xbb\xbf", false},
	} {
		for _, reader := range []io.Reader{strings.NewReader(test.input), iotest.OneByteReader(strings.NewReader(test.input))} {
			got, err := Import(reader)
			if (err == nil) != test.valid || err != nil && got != nil {
				t.Errorf("Import(%q) = %+v, %v; valid = %v", test.input, got, err, test.valid)
			}
		}
	}
}

func TestImportLexicalCharacters(t *testing.T) {
	for _, test := range []struct {
		name, data string
		valid      bool
	}{
		{"ASCII", "text & < > ' =", true},
		{"XML whitespace", "\t\r\n", true},
		{"Unicode", "\u00e9\u65e5\U0001f642", true},
		{"XML range edges", "\u0020\ud7ff\ue000\ufffd\U00010000\U0010ffff", true},
		{"BOM as content", "\ufeff", true},
		{"invalid UTF8", "\xff", false},
		{"truncated UTF8", "\xe2\x82", false},
		{"overlong UTF8", "\xc0\xaf", false},
		{"surrogate UTF8", "\xed\xa0\x80", false},
		{"out of range UTF8", "\xf4\x90\x80\x80", false},
		{"NUL", "\x00", false},
		{"control", "\x01", false},
		{"vertical tab", "\x0b", false},
		{"form feed", "\x0c", false},
		{"FFFE", "\ufffe", false},
		{"FFFF", "\uffff", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, token := range []string{"<!--" + test.data + "-->", "<?p " + test.data + "?>"} {
				for _, input := range []string{token + "<opml/>", "<opml/>" + token, "<opml><body>text" + token + "</body></opml>"} {
					for _, reader := range []io.Reader{strings.NewReader(input), iotest.OneByteReader(strings.NewReader(input))} {
						got, err := Import(reader)
						if (err == nil) != test.valid || err != nil && got != nil {
							t.Errorf("Import(%q) = %+v, %v; valid = %v", input, got, err, test.valid)
						}
					}
				}
			}
		})
	}
	// Put a multibyte character across the buffered reader's refill boundary.
	for _, data := range []string{"\u65e5", "\U0001f642", "\ufffe", "\xe2\x82"} {
		for _, prefix := range []string{"<opml/><!--", "<opml/><?p "} {
			suffix := "-->"
			if strings.Contains(prefix, "<?") {
				suffix = "?>"
			}
			input := prefix + strings.Repeat("a", 4095-len(prefix)) + data + suffix
			_, err := Import(strings.NewReader(input))
			if valid := data == "\u65e5" || data == "\U0001f642"; (err == nil) != valid {
				t.Errorf("boundary data %q: %v, valid = %v", data, err, valid)
			}
		}
	}
}

func TestImportProcessingInstructions(t *testing.T) {
	for _, test := range []struct {
		token string
		valid bool
	}{
		{"<?p?>", true},
		{"<?p ?>", true},
		{"<?p\r\n=bad?>", true},
		{"<?\u65e5 \u00e9?>", true},
		{"<?p \u00a0?>", true},
		{"<?p=bad?>", false},
		{"<?p/bad?>", false},
		{"<?p?bad?>", false},
		{"<?p\x00?>", false},
		{"<?xml?>", false},
		{"<?XmL?>", false},
	} {
		for _, input := range []string{test.token + "<opml/>", "<opml/>" + test.token, "<opml>text" + test.token + "</opml>"} {
			for _, reader := range []io.Reader{strings.NewReader(input), iotest.OneByteReader(strings.NewReader(input))} {
				if got, err := Import(reader); (err == nil) != test.valid || err != nil && got != nil {
					t.Errorf("Import(%q) = %+v, %v; valid = %v", input, got, err, test.valid)
				}
			}
		}
	}
}

func TestImportDeclaration(t *testing.T) {
	for _, declaration := range []string{`<?xml version="1.0"?>`, `<?xml version = '1.0' encoding = 'uTf-8' standalone = 'no' ?>`, "<?xml\r\nversion='1.0'\tstandalone='yes'?>"} {
		if _, err := Import(iotest.OneByteReader(strings.NewReader("\xef\xbb\xbf" + declaration + "<opml/>"))); err != nil {
			t.Errorf("declaration %q: %v", declaration, err)
		}
	}
	for _, declaration := range []string{`<?xml?>`, `<?xml version='1.0'junk?>`, `<?xml version='1.0'encoding='UTF-8'?>`, `<?xml version='1.0' version='1.0'?>`, `<?xml encoding='UTF-8'?>`, `<?xml encoding='UTF-8' version='1.0'?>`, `<?xml version='1.0' standalone='maybe'?>`, `<?xml version='1.0' standalone='yes' encoding='UTF-8'?>`, `<?xml version='1.0' unknown='x'?>`, `<?xml version='1.0"?>`} {
		if _, err := Import(strings.NewReader(declaration + "<opml/>")); err == nil {
			t.Errorf("accepted declaration %q", declaration)
		}
	}
	if _, err := Import(strings.NewReader(`<opml><?xml version="1.0"?></opml>`)); err == nil {
		t.Fatal("accepted declaration inside root")
	}
}

func TestImportDoctypePolicy(t *testing.T) {
	for _, test := range []struct {
		doctype string
		valid   bool
	}{
		{`<!DOCTYPE opml>`, true},
		{`<!DOCTYPE opml SYSTEM "https://example.com/opml.dtd">`, true},
		{`<!DOCTYPE opml PUBLIC '-//Example//DTD OPML 2.0//EN' 'opml.dtd' >`, true},
		{"<!DOCTYPE\topml\nSYSTEM '\u65e5.dtd'\r>", true},
		{`<!DOCTYPE opml nonsense>`, false},
		{`<!DOCTYPE other>`, false},
		{`<!DOCTYPE opml SYSTEM>`, false},
		{`<!DOCTYPE opml SYSTEM "opml.dtd"junk>`, false},
		{`<!DOCTYPE opml PUBLIC 'public id'>`, false},
		{`<!DOCTYPE opml PUBLIC 'bad^id' 'opml.dtd'>`, false},
		{`<!DOCTYPE opml [<!ELEMENT opml ANY>]>`, false},
		{`<!DOCTYPE opml []>`, false},
		{`<!DOCTYPE opml<!--comment-->>`, false},
		{"<!DOCTYPE opml SYSTEM '\xff'>", false},
	} {
		for _, reader := range []io.Reader{strings.NewReader(test.doctype + "<opml/>"), iotest.OneByteReader(strings.NewReader(test.doctype + "<opml/>"))} {
			if got, err := Import(reader); (err == nil) != test.valid || err != nil && got != nil {
				t.Errorf("DOCTYPE %q: %+v, %v; valid = %v", test.doctype, got, err, test.valid)
			}
		}
	}
	if _, err := Import(strings.NewReader(`<opml><!DOCTYPE opml></opml>`)); err == nil {
		t.Fatal("accepted DOCTYPE inside root")
	}
}

type importReader struct {
	io.Reader
	read int
	eof  bool
}

func (r *importReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.read += n
	r.eof = r.eof || err == io.EOF
	return n, err
}

func TestImportLimit(t *testing.T) {
	for _, trailing := range []bool{false, true} {
		for _, size := range []int{maxImport - 1, maxImport, maxImport + 1, maxImport + 4096} {
			prefix, suffix := "<opml><!--", "--></opml>"
			if trailing {
				prefix, suffix = "\xef\xbb\xbf"+xml.Header+"<opml/><!--", "-->"
			}
			reader := &importReader{Reader: io.MultiReader(strings.NewReader(prefix), strings.NewReader(strings.Repeat(" ", size-len(prefix)-len(suffix))), strings.NewReader(suffix))}
			got, err := Import(reader)
			if size <= maxImport {
				if err != nil || !reader.eof || reader.read != size {
					t.Fatalf("size %d trailing %v: %v, read %d, EOF %v", size, trailing, err, reader.read, reader.eof)
				}
			} else if err == nil || !strings.Contains(err.Error(), "too large") || got != nil || reader.read != maxImport+1 {
				t.Fatalf("size %d trailing %v: %+v, %v, read %d", size, trailing, got, err, reader.read)
			}
		}
	}
}

type onceErrorReader struct {
	io.Reader
	err error
}

func (r *onceErrorReader) Read(p []byte) (int, error) {
	if r.err != nil {
		err := r.err
		r.err = nil
		return 0, err
	}
	return r.Reader.Read(p)
}

func TestImportBOMSniffPreservesReaderError(t *testing.T) {
	sentinel := errors.New("one-shot input failure")
	const input = "<opml/>"
	for n := range 3 {
		reader := io.MultiReader(strings.NewReader(input[:n]), &onceErrorReader{Reader: strings.NewReader(input[n:]), err: sentinel})
		if got, err := Import(reader); !errors.Is(err, sentinel) || got != nil {
			t.Errorf("error after %d bytes: Import = %+v, %v", n, got, err)
		}
	}
}

func TestImportReaderErrors(t *testing.T) {
	sentinel := errors.New("input failed")
	for _, input := range []string{"", "<opml>", "<opml/>", "<opml/> <!--comment-->", "<opml/>" + strings.Repeat(" ", maxImport-len("<opml/>"))} {
		got, err := Import(io.MultiReader(strings.NewReader(input), iotest.ErrReader(sentinel)))
		if !errors.Is(err, sentinel) || got != nil {
			t.Fatalf("input length %d: Import() = %+v, %v", len(input), got, err)
		}
	}
	if _, err := Import(iotest.DataErrReader(strings.NewReader("<opml/>"))); err != nil {
		t.Fatalf("data with EOF: %v", err)
	}
}

func TestExportRoundTrip(t *testing.T) {
	for _, feeds := range [][]domain.Feed{nil, {}, {{Title: `A & "B"`, URL: "https://example.com/feed?a=1&b=2", SiteURL: "https://example.com"}, {Title: "Other", URL: "https://other.test/feed"}}} {
		var output bytes.Buffer
		if err := Export(&output, feeds); err != nil {
			t.Fatal(err)
		}
		got, err := Import(&output)
		if err != nil || len(got) != len(feeds) {
			t.Fatalf("round trip = %+v, %v", got, err)
		}
		for i, feed := range feeds {
			if got[i] != (Subscription{Title: feed.Title, FeedURL: feed.URL, SiteURL: feed.SiteURL}) {
				t.Fatalf("subscription %d = %+v", i, got[i])
			}
		}
	}
}

func FuzzImport(f *testing.F) {
	for _, input := range []string{"<opml/>", `<opml><body><outline text="Feed" xmlUrl="https://example.com/feed"/></body></opml>`, "<opml/><!--comment--><?style test?>", "<opml/><other/>", "<opml/><!--", "<opml/>&#32;", "<opml/><![CDATA[]]>",
		"\xef\xbb\xbf" + xml.Header + "<opml/>", "<opml/><!--\xff-->", "<opml/><?p \x00?>", "<opml/><?p=bad?>", "<?xml?><opml/>", "<!DOCTYPE opml nonsense><opml/>",
		"<opml/><!--\u65e5\U0001f642--><?\u65e5 \u00e9?>", "<opml><!--\xed\xa0\x80--></opml>", "<opml><?p=bad?></opml>", `<!DOCTYPE opml SYSTEM "opml.dtd"><opml/>`,
	} {
		f.Add(input)
	}
	f.Fuzz(func(t *testing.T, input string) {
		reader := &importReader{Reader: strings.NewReader(input)}
		got, err := Import(reader)
		if reader.read > maxImport+1 {
			t.Fatalf("read %d bytes", reader.read)
		}
		if err != nil {
			if got != nil {
				t.Fatal("partial subscriptions returned on error")
			}
			return
		}
		if !reader.eof || len(input) > maxImport {
			t.Fatal("accepted input without bounded actual EOF")
		}
		if !utf8.ValidString(input) {
			t.Fatal("accepted invalid UTF-8")
		}
		for _, ch := range input {
			if ch < 0x20 && ch != '\t' && ch != '\n' && ch != '\r' || ch == '\ufffe' || ch == '\uffff' {
				t.Fatalf("accepted invalid XML character %U", ch)
			}
		}
		if _, err := Import(strings.NewReader(input + "<opml/>")); err == nil {
			t.Fatal("accepted a second root")
		}
	})
}
