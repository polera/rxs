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

func TestTextTreatsTaglessContentAsPreformatted(t *testing.T) {
	fragment := "First line\n  indented line\nLast line"
	want := "    First line\n      indented line\n    Last line"
	if got := Text(fragment); got != want {
		t.Fatalf("Text() = %q, want %q", got, want)
	}

	got, links := TextWithLinks(fragment, "https://example.test/article", nil)
	if got != want {
		t.Fatalf("TextWithLinks() = %q, want %q", got, want)
	}
	if len(links) != 0 {
		t.Fatalf("TextWithLinks() links = %#v, want none", links)
	}
}

func TestTextTreatsPlaintextAngleBracketNotationAsPreformatted(t *testing.T) {
	fragment := "Branch/path      Hash\n- ------------------------\nSee <URL:https://security.example/>.\n# git show <commit hash>"
	want := "    Branch/path      Hash\n    - ------------------------\n    See <URL:https://security.example/>.\n    # git show <commit hash>"
	if got := Text(fragment); got != want {
		t.Fatalf("Text() = %q, want %q", got, want)
	}

	got, links := TextWithLinks(fragment, "https://example.test/article", nil)
	if got != want {
		t.Fatalf("TextWithLinks() = %q, want %q", got, want)
	}
	if len(links) != 0 {
		t.Fatalf("TextWithLinks() links = %#v, want none", links)
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

func TestTextFormatsCompactArticleBlocks(t *testing.T) {
	compact := `<div><p>First paragraph.</p><p>Second paragraph.</p><h3>The approaches</h3><h4>Control</h4><p>Body text.</p></div>`
	pretty := `<div>
		<p>First paragraph.</p>
		<p>Second paragraph.</p>
		<h3>The approaches</h3>
		<h4>Control</h4>
		<p>Body text.</p>
	</div>`
	want := "First paragraph.\n\nSecond paragraph.\n\n## The approaches\n\n### Control\n\nBody text."
	if got := Text(compact); got != want {
		t.Fatalf("Text(compact) = %q, want %q", got, want)
	}
	if got := Text(pretty); got != want {
		t.Fatalf("Text(pretty) = %q, want %q", got, want)
	}
}

func TestTextFormatsListsAndBlockquotes(t *testing.T) {
	fragment := `<ol start="3"><li>Third<ul><li>Nested</li></ul></li><li><p>Fourth</p><p>More detail</p></li></ol><blockquote><p>Quoted once.</p><p>Quoted twice.</p></blockquote>`
	want := "3. Third\n  • Nested\n4. Fourth\n\n  More detail\n\n│ Quoted once.\n│\n│ Quoted twice."
	if got := Text(fragment); got != want {
		t.Fatalf("Text() = %q, want %q", got, want)
	}
}

func TestTextPreservesCodeAndImageDescriptions(t *testing.T) {
	fragment := "<pre><code>if ready {\n  run()\n}</code></pre><p>Use <code>run()</code>.</p><p><img alt=\"A system diagram\" src=\"diagram.png\"></p>"
	want := "    if ready {\n      run()\n    }\n\nUse `run()`.\n\n[Image: A system diagram]"
	if got := Text(fragment); got != want {
		t.Fatalf("Text() = %q, want %q", got, want)
	}
}

func TestTextFormatsHeaderTableAsLabeledRows(t *testing.T) {
	fragment := `<table><thead><tr><th>Topic</th><th>Control</th><th>Emergence</th></tr></thead><tbody><tr><td><p>Training</p></td><td><p>Defined curriculum</p></td><td><p>Exploration</p></td></tr><tr><td>Safety</td><td>Prevent failure</td><td>Foster recovery</td></tr></tbody></table>`
	want := "Topic: Training\nControl: Defined curriculum\nEmergence: Exploration\n\nTopic: Safety\nControl: Prevent failure\nEmergence: Foster recovery"
	if got := Text(fragment); got != want {
		t.Fatalf("Text() = %q, want %q", got, want)
	}
}

func TestTextPreservesLinksInTableValues(t *testing.T) {
	fragment := `<table><tr><th>Topic</th><th>Reference</th></tr><tr><td>Training</td><td><a href="/guide">Guide</a></td></tr></table>`
	got, links := TextWithLinks(fragment, "https://example.test/article", func(_ int, link Link, text string) string {
		return "<" + text + "|" + link.URL + ">"
	})
	want := "Topic: Training\nReference: <Guide|https://example.test/guide>"
	if got != want {
		t.Fatalf("TextWithLinks() = %q, want %q", got, want)
	}
	if len(links) != 1 || links[0] != (Link{Text: "Guide", URL: "https://example.test/guide"}) {
		t.Fatalf("TextWithLinks() links = %#v", links)
	}
}

func TestTextFormatsTableWithoutHeadersAsRows(t *testing.T) {
	fragment := `<table><tr><td>One</td><td>Two</td></tr><tr><td>Three</td><td>Four</td></tr></table>`
	want := "| One | Two |\n| Three | Four |"
	if got := Text(fragment); got != want {
		t.Fatalf("Text() = %q, want %q", got, want)
	}
}
