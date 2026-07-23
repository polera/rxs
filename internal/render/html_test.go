package render

import (
	"strings"
	"testing"
)

func TestText(t *testing.T) {
	got := Text(`<article><h1>Hello &amp; goodbye</h1><p>First <strong>paragraph</strong>.</p><ul><li>One</li><li>Two</li></ul><script>bad()</script></article>`)
	for _, want := range []string{"Hello & goodbye", "First paragraph.", "• One", "• Two"} {
		if !strings.Contains(got, want) {
			t.Errorf("Text() = %q, missing %q", got, want)
		}
	}
	if strings.Contains(got, "bad()") {
		t.Errorf("Text() retained script content: %q", got)
	}
}

func TestLinksResolvesRelativeURLsAndFiltersSchemes(t *testing.T) {
	fragment := `<p><a href="/docs/start"><strong>Start</strong> here</a>
		<a href="https://other.example/path">Other</a>
		<a href="HTTPS://upper.example/path">Upper</a>
		<a href="mailto:hello@example.test">Email</a>
		<a href="#details"></a></p>`
	got := Links(fragment, "https://example.test/articles/one")
	want := []Link{
		{Text: "Start here", URL: "https://example.test/docs/start"},
		{Text: "Other", URL: "https://other.example/path"},
		{Text: "Upper", URL: "https://upper.example/path"},
		{Text: "https://example.test/articles/one#details", URL: "https://example.test/articles/one#details"},
	}
	if len(got) != len(want) {
		t.Fatalf("Links() = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Errorf("Links()[%d] = %#v, want %#v", index, got[index], want[index])
		}
	}
}

func TestTextWithLinksFormatsAnchorTextInPlace(t *testing.T) {
	fragment := `<p>Read <a href="/related"><strong>the related</strong> article</a> next.</p>
		<p><a href="mailto:hello@example.test">Email us</a> instead.</p>`
	got, links := TextWithLinks(fragment, "https://example.test/articles/one", func(index int, link Link, text string) string {
		return "<" + text + "|" + link.URL + ">"
	})
	want := "Read <the related article|https://example.test/related> next.\n\nEmail us instead."
	if got != want {
		t.Fatalf("TextWithLinks() = %q, want %q", got, want)
	}
	if len(links) != 1 || links[0] != (Link{Text: "the related article", URL: "https://example.test/related"}) {
		t.Fatalf("TextWithLinks() links = %#v", links)
	}
}

func TestTextWithLinksWithoutFormatterMatchesText(t *testing.T) {
	fragment := `<p>Space<a href="https://example.test"> around </a>this link.</p>`
	got, _ := TextWithLinks(fragment, "", nil)
	if want := Text(fragment); got != want {
		t.Fatalf("TextWithLinks() = %q, Text() = %q", got, want)
	}
}
