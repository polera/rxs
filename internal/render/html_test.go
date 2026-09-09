package render

import (
	"fmt"
	"html"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
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

func TestTextWithLinksOrderingAndWhitespace(t *testing.T) {
	fragment := `<p>Before<a href="/same">  </a>after <a href="/same"><img alt="diagram"></a></p><blockquote><a href="/same"> first<br>second </a></blockquote><table><tr><th><a href="/ignored">Heading</a></th></tr><tr><td><a href="/math">$<b>x</b>^α$ <code>$y$</code> \(\$z\$\)</a></td></tr></table><p><a href="/last"></a></p>`
	var indices []int
	var labels []string
	got, links := TextWithLinks(fragment, "https://example.test/article", func(index int, link Link, text string) string {
		indices = append(indices, index)
		labels = append(labels, text)
		return fmt.Sprintf("[%d:%s]", index, text)
	})
	want := "Before [0:https://example.test/same]after [1:[Image: diagram]]\n\n│  [2:first\n│ second] \n\nHeading: [3:x^(α) `$y$` $z$]\n\n[4:https://example.test/last]"
	if got != want {
		t.Fatalf("TextWithLinks() = %q, want %q", got, want)
	}
	if !reflect.DeepEqual(indices, []int{0, 1, 2, 3, 4}) {
		t.Fatalf("formatter indices = %v", indices)
	}
	if len(links) != 5 || links[0].URL != links[1].URL || links[1].URL != links[2].URL || links[1].Text != "diagram" {
		t.Fatalf("links = %#v", links)
	}
	if extracted := Links(fragment, "https://example.test/article"); !reflect.DeepEqual(extracted, links) {
		t.Fatalf("Links() = %#v, want %#v", extracted, links)
	}
	plain, _ := TextWithLinks(fragment, "https://example.test/article", nil)
	for i, label := range labels {
		got = strings.Replace(got, fmt.Sprintf("[%d:%s]", i, label), label, 1)
	}
	if plain != got {
		t.Fatalf("nil formatter = %q, want %q", plain, got)
	}
}

func TestFormatLinkMarkersWhitespace(t *testing.T) {
	for _, visible := range []string{"", " \t\n", " \tlabel\n", "\u2003label\u00a0", "first\nsecond"} {
		links := []Link{{Text: "fallback", URL: "https://example.test"}}
		input := "prefix" + linkMarker(0, "start") + visible + linkMarker(0, "end") + "suffix"
		core := strings.TrimSpace(visible)
		want := "prefix" + visible + "suffix"
		if core == "" {
			want = "prefix" + visible + "fallback" + "suffix"
		}
		if got := formatLinkMarkers(input, links, []bool{true}, nil); got != want {
			t.Errorf("visible %q: got %q, want %q", visible, got, want)
		}
	}
}

func TestTextWithLinksNestedAnchorTable(t *testing.T) {
	input := `<a href="/outer"><table><tr><td><a href="/inner">inside</a></td></tr></table>outside</a><a href="/last">last</a>`
	var indices []int
	got, links := TextWithLinks(input, "https://example.test", func(index int, _ Link, text string) string {
		indices = append(indices, index)
		return fmt.Sprintf("[%d:%s]", index, text)
	})
	want := "\n\n[0:| [1:inside] |\n\noutside][2:last]"
	if got != want || !reflect.DeepEqual(indices, []int{0, 1, 2}) || len(links) != 3 {
		t.Fatalf("TextWithLinks() = %q, indices %v, links %#v; want %q", got, indices, links, want)
	}
}

func TestTextWithLinksMathAcrossInlineNodes(t *testing.T) {
	const base = "https://example.test"
	for _, tt := range []struct {
		name, input, plain, marked string
		labels                     []string
	}{
		{"linked base", `<p>$<a href="/x">x</a>^2$</p>`, "x²", "[0:x]²", []string{"x"}},
		{"single linked variable", `<p>$<a href="/x">x</a>$</p>`, "x", "[0:x]", []string{"x"}},
		{"linked exponent", `<p>$x^<a href="/x">2</a>$</p>`, "x²", "x[0:²]", []string{"²"}},
		{"unicode exponent", `<p>$x^{<a href="/x">α</a>}$</p>`, "x^(α)", "x^([0:α])", []string{"α"}},
		{"multiple exponent links", `<p>$x^{<a href="/x">2</a><a href="/y">3</a>}$</p>`, "x²³", "x[0:²][1:³]", []string{"²", "³"}},
		{"fraction", `<p>\(\frac{<a href="/x">x</a>}{<a href="/y">y</a>}\)</p>`, "(x)/(y)", "([0:x])/([1:y])", []string{"x", "y"}},
		{"linked brace groups", `<p>\(\frac<a href="/x">{x}</a><a href="/y">{y}</a>\)</p>`, "(x)/(y)", "([0:x])/([1:y])", []string{"x", "y"}},
		{"command", `<p>\(<a href="/x">\alpha</a>+\beta\)</p>`, "α+β", "[0:α]+β", []string{"α"}},
		{"split command", `<p>\(\<a href="/x">alpha</a>\)</p>`, "α", "[0:α]", []string{"α"}},
		{"escaped symbol", `<p>\(\<a href="/x">%</a>\)</p>`, "%", "[0:%]", []string{"%"}},
		{"linked fraction command", `<p>\(<a href="/x">\frac</a>{1}{2}\)</p>`, "(1)/(2)", "[0:(1)/(2)]", []string{"(1)/(2)"}},
		{"root degree", `<p>\(\sqrt[<a href="/x">3</a>]{x}\)</p>`, "³√(x)", "[0:³]√(x)", []string{"³"}},
		{"implicit root degree", `<p>\(\sqrt[<a href="/x">2</a>]{x}\)</p>`, "√(x)", "[0:√](x)", []string{"√"}},
		{"accent linked base", `<p>\(\hat{<a href="/x">x</a>^2}\)</p>`, "x̂²", "[0:x̂]²", []string{"x̂"}},
		{"accent linked script", `<p>\(\hat{x^<a href="/x">2</a>}\)</p>`, "x̂²", "x̂[0:²]", []string{"²"}},
		{"linked accent expression", `<p>\(<a href="/x">\widehat{AB}</a>\)</p>`, "ÂB", "[0:ÂB]", []string{"ÂB"}},
		{"display br", `<p>\[<a href="/x">x</a>^2+<br><a href="/y">y</a>_1\]</p>`, "x²+\ny₁", "[0:x]²+\n[1:y]₁", []string{"x", "y"}},
		{"display dollars br", `<p>$$<a href="/x">x^2+<br>y_1</a>$$</p>`, "x²+\ny₁", "[0:x²+\ny₁]", []string{"x²+\ny₁"}},
		{"unlinked display br", `<p>\[x^2+<br>y_1\]</p>`, "x²+\ny₁", "x²+\ny₁", nil},
		{"parenthesized br", `<p>\(x^2+<br>y_1\)</p>`, "x²+\ny₁", "x²+\ny₁", nil},
		{"single dollars br remain literal", `<p>$x^2+<br>y_1$</p>`, "$x^2+\ny_1$", "$x^2+\ny_1$", nil},
		{"display trim", `<p>\[<a href="/x"> <br>x^2<br> </a>\]</p>`, "x²", "[0:x²]", []string{"x²"}},
		{"quote once", `<blockquote><p>\[<a href="/x">\$x\$</a><br>y^2\]</p></blockquote>`, "│ $x$\n│ y²", "│ [0:$x$]\n│ y²", []string{"$x$"}},
		{"table once", `<table><tr><td>\[<a href="/x">\$x\$</a><br>y^2\]</td></tr></table>`, "| $x$ y² |", "| [0:$x$] y² |", []string{"$x$"}},
		{"literal code boundary", `<p>$<a href="/x">x</a><code>$y$</code>^2$</p>`, "$x`$y$`^2$", "$[0:x]`$y$`^2$", []string{"x"}},
		{"literal linked code", `<p><a href="/x"><code>$x$</code></a> and $y^2$</p><pre>\[z^2\]</pre>`, "`$x$` and y²\n\n    \\[z^2\\]", "[0:`$x$`] and y²\n\n    \\[z^2\\]", []string{"`$x$`"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := Text(tt.input); got != tt.plain {
				t.Fatalf("Text() = %q, want %q", got, tt.plain)
			}
			plain, links := TextWithLinks(tt.input, base, nil)
			if plain != tt.plain || len(links) != len(tt.labels) {
				t.Fatalf("TextWithLinks(nil) = %q, %#v; want %q, %d links", plain, links, tt.plain, len(tt.labels))
			}
			var indices []int
			marked, formattedLinks := TextWithLinks(tt.input, base, func(index int, link Link, text string) string {
				indices = append(indices, index)
				if index >= len(tt.labels) || text != tt.labels[index] || link != links[index] {
					t.Fatalf("formatter(%d, %#v, %q)", index, link, text)
				}
				return fmt.Sprintf("[%d:%s]", index, text)
			})
			if marked != tt.marked || !reflect.DeepEqual(formattedLinks, links) || len(indices) != len(links) {
				t.Fatalf("formatted = %q, indices %v, links %#v; want %q", marked, indices, formattedLinks, tt.marked)
			}
			oscFormatter := func(index int, link Link, text string) string {
				return fmt.Sprintf("\x1b]8;id=rxs-%d;%s\x1b\\%s\x1b]8;;\x1b\\", index, link.URL, text)
			}
			wantOSC := tt.marked
			for i, link := range links {
				if indices[i] != i || link.URL != base+[]string{"/x", "/y"}[i] {
					t.Fatalf("link identity/order changed: %v, %#v", indices, links)
				}
				wantOSC = strings.Replace(wantOSC, fmt.Sprintf("[%d:%s]", i, tt.labels[i]), oscFormatter(i, link, tt.labels[i]), 1)
			}
			if osc, _ := TextWithLinks(tt.input, base, oscFormatter); osc != wantOSC {
				t.Fatalf("OSC output = %q, want %q", osc, wantOSC)
			}
		})
	}
}

func TestTextWithLinksAdjacentMath(t *testing.T) {
	for _, tt := range []struct{ name, input, plain, marked string }{
		{"split command", `<p>\(<a href="/a">\al</a><a href="/b">pha</a>\)</p>`, "α", "[0:α][1:]"},
		{"three command links", `<p>\(\<a href="/a">al</a><a href="/b">ph</a><a href="/c">a</a>\)</p>`, "α", "[0:α][1:][2:]"},
		{"removed label", `<p>\(\label{<a href="/x">foo</a>}x\)</p>`, "x", "[0:]x"},
		{"split removed macro and argument", `<p>\(\<a href="/a">la</a><a href="/b">bel</a>{<a href="/c">foo</a>}x\)</p>`, "x", "[0:][1:][2:]x"},
		{"two removed arguments", `<p>\(\label{<a href="/a">foo</a>}\tag{<a href="/b">bar</a>}<a href="/c">x</a>\)</p>`, "x", "[0:][1:][2:x]"},
		{"split root command", `<p>\(<a href="/a">\sq</a><a href="/b">rt</a>[3]{x}\)</p>`, "³√(x)", "[0:³√(x)][1:]"},
		{"root command degree and value", `<p>\(<a href="/a">\sqrt</a>[<a href="/b">2</a>]{<a href="/c">x</a>}\)</p>`, "√(x)", "[0:][1:√]([2:x])"},
		{"fraction command and arguments", `<p>\(<a href="/a">\fr</a><a href="/b">ac</a>{<a href="/c">x</a>}{<a href="/d">y</a>}\)</p>`, "(x)/(y)", "[0:(][1:][2:x])/([3:y])"},
		{"nested quote", `<blockquote><p>\(<a href="/a">\al</a><a href="/b">pha</a>\label{<a href="/c">foo</a>}\)</p></blockquote>`, "│ α", "│ [0:α][1:][2:]"},
		{"table", `<table><tr><td>\(\label{<a href="/a">foo</a>}<a href="/b">x</a>\)</td></tr></table>`, "| x |", "| [0:][1:x] |"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			plain, links := TextWithLinks(tt.input, "https://example.test", nil)
			if plain != tt.plain || plain != Text(tt.input) {
				t.Fatalf("TextWithLinks(nil) = %q, Text = %q; want %q", plain, Text(tt.input), tt.plain)
			}
			called := 0
			marked, _ := TextWithLinks(tt.input, "https://example.test", func(index int, link Link, text string) string {
				if index != called || link != links[index] || strings.ContainsRune(text, '\x00') {
					t.Fatalf("formatter(%d, %#v, %q), want index %d without markers", index, link, text, called)
				}
				called++
				return fmt.Sprintf("[%d:%s]", index, text)
			})
			if marked != tt.marked || called != len(links) {
				t.Fatalf("formatted = %q, callbacks %d/%d; want %q", marked, called, len(links), tt.marked)
			}
		})
	}
}

func TestTextWithLinksSourceEmptyFallback(t *testing.T) {
	input := `<p><a href="/empty"></a> <a href="/space"> </a> <a href="/image"><img></a> <a href="/alt"><img alt="diagram"></a> \(\label{<a href="/erased">foo</a>}x\)</p>`
	var labels []string
	got, links := TextWithLinks(input, "https://example.test", func(_ int, _ Link, text string) string {
		labels = append(labels, text)
		return text
	})
	want := []string{"https://example.test/empty", "https://example.test/space", "[Image]", "[Image: diagram]", ""}
	if !reflect.DeepEqual(labels, want) || len(links) != len(want) || links[4].Text != "foo" {
		t.Fatalf("labels = %#v, links = %#v", labels, links)
	}
	if strings.Contains(got, "foo") || strings.ContainsRune(got, '\x00') || !strings.HasSuffix(got, " x") {
		t.Fatalf("invalid output: %q", got)
	}
	if plain, _ := TextWithLinks(input, "https://example.test", nil); plain != got {
		t.Fatalf("nil formatter = %q, want %q", plain, got)
	}
}

func TestTextWithLinksMathPartitions(t *testing.T) {
	for _, expression := range []string{
		`\(\alpha\)`,
		`\(\label{foo}\tag{bar}x\)`,
		`\(\frac{\alpha_2}{\sqrt[2]{y^3}}\)`,
		`\(\sqrt[3]{x}+\sqrt[2]{y}\)`,
		`\(\begin{matrix}x\end{matrix}\)`,
		`\(\left(x\right)+\!y\)`,
		`\(x^{α}+\fracαβ\)`,
		`\(\hat{x^2}+\vec{V_A}+\widehat{AB}\)`,
		`\(\$x\$\)`,
	} {
		for _, size := range []int{1, 2, 3, 5} {
			t.Run(fmt.Sprintf("%s/chunk=%d", expression, size), func(t *testing.T) {
				var fragment strings.Builder
				fragment.WriteString("<p>")
				runes := []rune(expression)
				count := 0
				for len(runes) > 0 {
					end := min(size, len(runes))
					fmt.Fprintf(&fragment, `<a href="/%d">%s</a>`, count, html.EscapeString(string(runes[:end])))
					count++
					runes = runes[end:]
				}
				fragment.WriteString("</p>")
				plain, links := TextWithLinks(fragment.String(), "https://example.test", nil)
				if want := Text(fragment.String()); plain != want || len(links) != count {
					t.Fatalf("TextWithLinks(nil) = %q, want Text = %q; links %d/%d", plain, want, len(links), count)
				}
				called := 0
				osc, _ := TextWithLinks(fragment.String(), "https://example.test", func(index int, link Link, text string) string {
					if index != called || link != links[index] || link.URL != fmt.Sprintf("https://example.test/%d", index) || strings.ContainsRune(text, '\x00') {
						t.Fatalf("invalid callback %d: %#v %q (expected %d)", index, link, text, called)
					}
					called++
					return "\x1b]8;;" + link.URL + "\x1b\\" + text + "\x1b]8;;\x1b\\"
				})
				if called != count || strings.ContainsRune(osc, '\x00') || strings.Count(osc, "\x1b]8;;https://example.test/") != count || strings.Count(osc, "\x1b]8;;\x1b\\") != count {
					t.Fatalf("invalid OSC output %q; callbacks %d/%d", osc, called, count)
				}
			})
		}
	}
}

func TestTextRendersMathObjectsAndImages(t *testing.T) {
	tests := []struct {
		name, input, want string
	}{
		{"Atom inline object", `<p>velocity <object class="valign-middle latex-math" data="https://eli.thegreenplace.net/images/math/vector.svg">\vec{V_A} &lt; \mathbb{R}</object>.</p>`, `velocity V⃗_(A) < ℝ.`},
		{"Atom display object", `<p>before<object class="align-center" type="image/svg+xml">\[\begin{aligned}a &amp;= b\\c &amp;= d\end{aligned}\]</object>after</p>`, "before\n\na = b\nc = d\n\nafter"},
		{"math URL object", `<p><object data="/images/math/fourier.svg">\hat f=\mathcal{F}(f)</object></p>`, `f̂=ℱ(f)`},
		{"delimiter fallback object", `<p><object data="other.svg">\(x^2\)</object></p>`, `x²`},
		{"math class image", `<p><img class="latex-math" src="formula.svg" alt="\vec{V_A}"></p>`, `V⃗_(A)`},
		{"display math image class", `<p>before<img class="latex-math align-center" src="formula.svg" alt="\hat{x^2}">after</p>`, "before\n\nx̂²\n\nafter"},
		{"display math image delimiters", `<p>before<img src="formula.svg" alt="\[x^2\]">after</p>`, "before\n\nx²\n\nafter"},
		{"math URL image", `<p><img src="/images/math/dot.svg" alt="\langle a,b\rangle"></p>`, `⟨a,b⟩`},
		{"empty math image", `<p><img class="latex-math" src="formula.svg"></p>`, `[Image]`},
		{"empty math object", `<object class="latex-math"> 	 </object>`, ``},
		{"ordinary image", `<p><img src="diagram.svg" alt="\vec{x}"></p>`, `[Image: \vec{x}]`},
		{"ordinary object", `<p><object data="document.bin">ordinary <b>fallback</b></object></p>`, `ordinary fallback`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Text(tt.input); got != tt.want {
				t.Fatalf("Text() = %q, want %q", got, tt.want)
			}
			if got, _ := TextWithLinks(tt.input, "https://example.test/article", nil); got != tt.want {
				t.Fatalf("TextWithLinks(nil) = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTextMathObjectPreservesFallbackLink(t *testing.T) {
	fragment := `<p><object class="latex-math" data="/images/math/vector.svg"><a href="/vector">\vec{V_A}</a></object></p>`
	plain, links := TextWithLinks(fragment, "https://example.test/article", nil)
	if plain != "V⃗_(A)" || plain != Text(fragment) {
		t.Fatalf("plain output = %q, Text = %q", plain, Text(fragment))
	}
	if len(links) != 1 || links[0] != (Link{Text: `\vec{V_A}`, URL: "https://example.test/vector"}) {
		t.Fatalf("links = %#v", links)
	}
	formatted, formattedLinks := TextWithLinks(fragment, "https://example.test/article", func(index int, link Link, text string) string {
		if index != 0 || link != links[0] || text != "V⃗_(A)" {
			t.Fatalf("formatter(%d, %#v, %q)", index, link, text)
		}
		return "[" + text + "]"
	})
	if formatted != "[V⃗_(A)]" || !reflect.DeepEqual(formattedLinks, links) {
		t.Fatalf("formatted = %q, links %#v", formatted, formattedLinks)
	}
}

func TestTextDisplayMathImagePreservesLink(t *testing.T) {
	fragment := `<p>before<a href="/formula"><img class="latex-math align-center" alt="\[\hat{x^2}\]"></a>after</p>`
	want := "before\n\nx̂²\n\nafter"
	plain, links := TextWithLinks(fragment, "https://example.test/article", nil)
	if plain != want || plain != Text(fragment) {
		t.Fatalf("plain = %q, Text = %q, want %q", plain, Text(fragment), want)
	}
	if len(links) != 1 || links[0] != (Link{Text: `\[\hat{x^2}\]`, URL: "https://example.test/formula"}) {
		t.Fatalf("links = %#v", links)
	}
	marked, _ := TextWithLinks(fragment, "https://example.test/article", func(index int, link Link, text string) string {
		if index != 0 || link != links[0] || text != "x̂²" {
			t.Fatalf("formatter(%d, %#v, %q)", index, link, text)
		}
		return "[" + text + "]"
	})
	if marked != "before\n\n[x̂²]\n\nafter" {
		t.Fatalf("formatted = %q", marked)
	}
}

func FuzzHTML(f *testing.F) {
	for _, seed := range []string{`<p>$x^α$ <code>$y$</code></p>`, `<blockquote><a href="/x">\(\$x\$\)</a></blockquote>`, `<table><tr><td><a href="/x"></a></td></tr></table>`, `<pre>\(α\)</pre>`, `<p>$<b>x</b>^2$</p>`, `<a href="/outer"><table><tr><td><a href="/inner">inside</a></td></tr></table></a>`, `<p>$<a href="/x">x</a>^2$</p>`, `<p>\[<a href="/x">x^2<br>y_1</a>\]</p>`, `<p>\(\frac{<a href="/x">α</a>}{<a href="/y">β</a>}\)</p>`} {
		f.Add(seed)
	}
	f.Add(`<p>\(<a href="/a">\al</a><a href="/b">pha</a>\)</p>`)
	f.Add(`<p>\(\label{<a href="/x">foo</a>}x\)</p>`)
	f.Add(`<p>\(<a href="/a">\sqrt</a>[<a href="/b">2</a>]{<a href="/c">x</a>}\)</p>`)
	f.Add(`<p><object class="latex-math" data="/images/math/x.svg"><a href="/x">\vec{V_A}</a></object><img class="latex-math" alt="\mathbb R"></p>`)
	f.Add("<object class=\"latex-math\"> \t </object>")
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 16384 || !utf8.ValidString(input) || strings.ContainsRune(input, '\x00') {
			t.Skip()
		}
		if got := Text(input); !utf8.ValidString(got) {
			t.Fatalf("Text() returned invalid UTF-8: %q", got)
		}
		var indices []int
		got, links := TextWithLinks(input, "https://example.test/article", func(index int, link Link, text string) string {
			indices = append(indices, index)
			if !utf8.ValidString(text) || !utf8.ValidString(link.Text) || !utf8.ValidString(link.URL) {
				t.Fatal("invalid UTF-8 link")
			}
			return text
		})
		if !utf8.ValidString(got) || strings.Contains(got, "\x00rxs-link-") {
			t.Fatalf("invalid rendered output: %q", got)
		}
		if len(indices) != len(links) {
			t.Fatalf("formatted %d of %d links", len(indices), len(links))
		}
		for i, index := range indices {
			if i != index {
				t.Fatalf("link index %d at position %d", index, i)
			}
		}
		literal := strings.Join(strings.Fields(input), " ")
		want := "beforeafter"
		if literal != "" {
			want = "before`" + literal + "`after"
		}
		if got := Text("<p>before<code>" + html.EscapeString(input) + "</code>after</p>"); got != want {
			t.Fatalf("code changed: got %q, want %q", got, want)
		}
	})
}
