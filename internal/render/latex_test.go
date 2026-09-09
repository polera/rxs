package render

import (
	"fmt"
	"html"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

func TestLaTeXRendersDelimitedMath(t *testing.T) {
	input := `Euler wrote $e^{i\pi}+1=0$, while \(x_1 \le x_2\) and \[\frac{-b \pm \sqrt{b^2-4ac}}{2a}\].`
	want := `Euler wrote e^(iπ)+1=0, while x₁ ≤ x₂ and (-b ± √(b²-4ac))/(2a).`
	if got := LaTeX(input); got != want {
		t.Fatalf("LaTeX() = %q, want %q", got, want)
	}
}

func TestLaTeXDoesNotTreatCurrencyAsMath(t *testing.T) {
	input := `The first plan costs $5 and the other costs $10.`
	if got := LaTeX(input); got != input {
		t.Fatalf("LaTeX() changed currency: %q", got)
	}
}

func TestLaTeXLeavesUnmatchedDelimiterAlone(t *testing.T) {
	input := `An unmatched $x and an escaped \$5.`
	if got := LaTeX(input); got != input {
		t.Fatalf("LaTeX() = %q, want unchanged input", got)
	}
}

func TestTextRendersFeedLaTeX(t *testing.T) {
	fragment := `<p>The result is $E=mc^2$ and \(\alpha + \beta\).</p>`
	want := `The result is E=mc² and α + β.`
	if got := Text(fragment); got != want {
		t.Fatalf("Text() = %q, want %q", got, want)
	}
}

func TestTextRendersMathJaxScript(t *testing.T) {
	fragment := `<p>Inline <script type="math/tex">x_i^2</script>.</p><script type="math/tex; mode=display">\sum_{i=1}^n i</script><script>bad()</script>`
	want := "Inline xᵢ².\n\n∑ᵢ₌₁ⁿ i"
	if got := Text(fragment); got != want {
		t.Fatalf("Text() = %q, want %q", got, want)
	}
}

func TestLaTeXUnicodeAndDelimiters(t *testing.T) {
	for _, tt := range []struct{ input, want string }{
		{`$x^α + y_界$`, `x^(α) + y_(界)`},
		{`\(\fracαβ + \sqrt界\)`, `(α)/(β) + √(界)`},
		{"\\(\\frac\u2003α\u00a0β + x^\u202f2\\)", `(α)/(β) + x²`},
		{`\(\alphaβ + \unknown{界} + \β\)`, `αβ + \unknown界 + \β`},
		{`\(x^🙂\)`, `x^(🙂)`},
		{`\$x$ and $5 or $10 and $x$`, `\$x$ and $5 or $10 and x`},
		{`\\$x$`, `\\x`},
		{`\\\$x$`, `\\\$x$`},
		{`\(a\) \[b\] $$c$$`, `a b c`},
		{`\(\$x\$\)`, `$x$`},
		{`$ x $ and $5$ and $x$10`, `$ x $ and $5$ and $x$10`},
		{`\(unmatched $x$`, `\(unmatched x`},
		{`\(\) $$ $$`, `\(\) $$ $$`},
		{`$x\$y$`, `x$y`},
		{`\(a\\)b\)`, `a )b`},
	} {
		t.Run(tt.input, func(t *testing.T) {
			if got := LaTeX(tt.input); got != tt.want {
				t.Fatalf("LaTeX(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestLaTeXDeepNesting(t *testing.T) {
	for _, expression := range []string{
		strings.Repeat("{", 10000) + "α" + strings.Repeat("}", 10000),
		strings.Repeat(`\sqrt`, 10000) + "α",
	} {
		got := LaTeX(`\(` + expression + `\)`)
		if !utf8.ValidString(got) || !strings.Contains(got, "α") {
			t.Fatal("deep expression lost its Unicode argument")
		}
	}
}

func TestTextMathRegions(t *testing.T) {
	for _, tt := range []struct{ name, input, want string }{
		{"inline code", `<p>Use <code>$x$ \(y\) $$z$$</code> then $x^2$.</p>`, "Use `$x$ \\(y\\) $$z$$` then x²."},
		{"pre", "<pre><code>$x$\n  \\(y\\)</code></pre><p>$z$</p>", "    $x$\n      \\(y\\)\n\nz"},
		{"tagless pre", `$x$ \(y\)`, `    $x$ \(y\)`},
		{"inline formatting", `<p>$<em>x</em><span>^</span><b>α</b>$ and \(<i>\frac</i><b>α</b>β\)</p>`, `x^(α) and (α)/(β)`},
		{"nested quote", `<blockquote><div><p>\(\$x\$\)</p><blockquote><p>\(\$y\$\) <code>$z$</code></p></blockquote></div></blockquote>`, "│ $x$\n│\n│ │ $y$ `$z$`"},
		{"script once", `<p><script type="math/tex">\$x\$</script> <script type="math/tex">\unknown</script></p>`, `$x$ \unknown`},
		{"header table", `<table><tr><th>\(\$x\$\)</th></tr><tr><td><p>\(\$y\$\) <code>$z$</code></p></td></tr><tr><td>$a^2$</td></tr></table>`, "$x$: $y$ `$z$`\n\n$x$: a²"},
		{"nested table", `<table><tr><td>\(\$x\$\)<table><tr><td>\(\$y\$\)</td></tr></table></td><td><code>$z$</code></td></tr></table>`, "| $x$ | $y$ | | `$z$` |"},
		{"block boundaries", `<p>$x</p><p>y$</p><p>\(a</p><p>b\)</p>`, "$x\n\ny$\n\n\\(a\n\nb\\)"},
		{"code boundary", `<p>$x<code>literal</code>^2$</p>`, "$x`literal`^2$"},
		{"cell boundary", `<table><tr><td>$x</td><td>y$</td></tr></table>`, `| $x | y$ |`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := Text(tt.input); got != tt.want {
				t.Fatalf("Text() = %q, want %q", got, tt.want)
			}
			if got, _ := TextWithLinks(tt.input, "https://example.test", nil); got != tt.want {
				t.Fatalf("TextWithLinks() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLaTeXEliCorpus(t *testing.T) {
	tests := []struct {
		name, input, want string
	}{
		{"relative velocity", `\(\vec{V_A}=v\widehat{u},\ \mathbb{R},\ \langle A,B\rangle\)`, `V⃗_(A)=vû, ℝ, ⟨A,B⟩`},
		{"Fourier transform", `\(\hat f(\xi)=\mathcal{F}(f),\ \boxed{x\ast y},\ \bigg(\frac{a}{b}\bigg)\)`, `f̂(ξ)=ℱ(f), [x∗ y], ((a)/(b))`},
		{"dot product", `\(\vec a\cdot\vec b\in\mathbb R,\ \langle a,b\rangle,\ \dots\ \blacksquare\)`, `a⃗·b⃗∈ℝ, ⟨a,b⟩, … ■`},
		{"Fourier series cases", `\[f(n)=\begin{cases}1 & \text{if }n=0\\0 & \text{if }n\ne0\end{cases}\]`, "f(n)=1  if n=0\n0  if n≠0"},
		{"sinusoids", `\(\sin(x)=0\implies x=k\pi\)`, `sin(x)=0⇒ x=kπ`},
		{"factorial", `\(n!=\left.\frac{d^n}{dx^n}x^n\right|_{x=0}\)`, `n!=(dⁿ)/(dxⁿ)xⁿ|ₓ₌₀`},
		{"aligned display", `\[\begin{aligned}a &= b\\c &= d\end{aligned}\]`, "a = b\nc = d"},
		{"sizing variants", `\(\bigl[x\bigr]+\Bigl(y\Bigr)+\biggl\{z\biggr\}\)`, `[x]+(y)+{z}`},
	}
	unsupported := []string{`\vec`, `\hat`, `\widehat`, `\mathbb`, `\mathcal`, `\langle`, `\rangle`, `\ast`, `\implies`, `\big`, `\boxed`, `\dots`, `\blacksquare`, `\begin`, `\end`, `\left`, `\right`}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LaTeX(tt.input)
			if got != tt.want {
				t.Fatalf("LaTeX() = %q, want %q", got, tt.want)
			}
			for _, command := range unsupported {
				if strings.Contains(got, command) {
					t.Errorf("output leaked supported corpus command %q: %q", command, got)
				}
			}
		})
	}
}

func TestLaTeXVectorUserCase(t *testing.T) {
	if got, want := LaTeX(`\(\vec{V_A}\)`), "V⃗_(A)"; got != want {
		t.Fatalf("LaTeX() = %q, want %q", got, want)
	}
}

func TestLaTeXAccentsDecorateBase(t *testing.T) {
	for _, tt := range []struct{ input, want string }{
		{`\(\vec{V_A}\)`, "V⃗_(A)"},
		{`\(\hat{x^2}\)`, "x̂²"},
		{`\(\widehat{AB}\)`, "ÂB"},
		{`\(\hat{x́^2}\)`, "x́̂²"},
	} {
		if got := LaTeX(tt.input); got != tt.want {
			t.Errorf("LaTeX(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestLaTeXNestedEnvironments(t *testing.T) {
	input := `\[\begin{cases}outer&one\\\begin{aligned}a&=b\\c&=d\end{aligned}\\last&three\end{cases}\]`
	want := "outer  one\na=b\nc=d\nlast  three"
	if got := LaTeX(input); got != want {
		t.Fatalf("LaTeX() = %q, want %q", got, want)
	}
}

func FuzzLaTeX(f *testing.F) {
	for _, seed := range []string{`$x^α$`, `\(\fracαβ\)`, "\\(x^\u20032\\)", `\(\unknown{界}\)`, `\$5 and $10`, strings.Repeat(`\(`, 100), strings.Repeat(`\`, 100), `$$a\\b$$`, "ordinary prose", `$$$x$$$$`, `\(a\\)b\)`, `\[\( $x$ \]`, `\(\vec{V_A}+\widehat{x}+\mathbb R+\mathcal F\)`, `\[\begin{cases}x&\text{if }y\\z&\text{if }w\end{cases}\]`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 32768 || !utf8.ValidString(input) {
			t.Skip()
		}
		for _, got := range []string{LaTeX(input), renderLaTeXExpression(input, false), renderLaTeXExpression(input, true)} {
			if !utf8.ValidString(got) {
				t.Fatalf("invalid UTF-8 output for %q: %q", input, got)
			}
		}
		if !strings.ContainsAny(input, `$\`) && LaTeX(input) != input {
			t.Fatal("delimiter-free prose changed")
		}
		if len(input) <= 512 {
			if got, want := LaTeX(input), referenceLaTeXDelimiters(input); got != want {
				t.Fatalf("scanner changed delimiter semantics: got %q, want %q", got, want)
			}
		}
	})
}

func FuzzMathLinkAnnotations(f *testing.F) {
	for _, input := range []string{`$x^2$`, `\(\alpha\)`, `\(\fracαβ\)`, `\[x^2\ny_1\]`, `\(\sqrt[2]{x}\)`, `\(\label{x}y\)`, `\(x^{α}\)`, `\(\$x\$\)`, `\(\vec{V_A}+\hat{x^2}+\mathbb R+\boxed{x}\)`, `\[\begin{cases}x&y\\\begin{aligned}a&=b\end{aligned}\\z&w\end{cases}\]`} {
		f.Add(input, uint16(2), uint16(len(input)-2))
	}
	f.Add(`\(\alpha\)`, uint16(5), uint16(8))
	f.Add(`\(\label{foo}x\)`, uint16(9), uint16(12))
	f.Add(`\(\sqrt[2]{x}\)`, uint16(7), uint16(10))
	f.Fuzz(func(t *testing.T, input string, start, end uint16) {
		if len(input) > 4096 || !utf8.ValidString(input) || strings.ContainsRune(input, '\x00') {
			t.Skip()
		}
		// Keep this target about math annotations, not the existing source-empty
		// anchor fallback and HTML whitespace-normalization policies.
		input = strings.Map(func(ch rune) rune {
			if unicode.IsSpace(ch) {
				return -1
			}
			return ch
		}, input)
		runes := []rune(input)
		from, to := int(start)%(len(runes)+1), int(end)%(len(runes)+1)
		if from > to {
			from, to = to, from
		}
		var annotated, fragment strings.Builder
		fragment.WriteString("<p>before:")
		count := 0
		for _, part := range []string{string(runes[:from]), string(runes[from:to]), string(runes[to:])} {
			if part == "" {
				continue
			}
			annotated.WriteString(linkMarker(count, "start") + part + linkMarker(count, "end"))
			fmt.Fprintf(&fragment, `<a href="/%d">%s</a>`, count, html.EscapeString(part))
			count++
		}
		fragment.WriteString(":after</p>")
		source, markers := splitMathMarkers(annotated.String())
		got := latex(source, markers)
		plain, resultMarkers := splitMathMarkers(got)
		if want := LaTeX(input); plain != want {
			t.Fatalf("annotation changed math: %q at %d:%d: got %q, want %q", input, from, to, plain, want)
		}
		if len(resultMarkers) != len(markers) || !utf8.ValidString(got) {
			t.Fatalf("annotations corrupted: %q", got)
		}
		for i := range markers {
			if resultMarkers[i].text != markers[i].text {
				t.Fatalf("annotation order changed: %q", got)
			}
		}
		want := Text(fragment.String())
		final, links := TextWithLinks(fragment.String(), "https://example.test", nil)
		if final != want || len(links) != count || strings.ContainsRune(final, '\x00') {
			t.Fatalf("TextWithLinks(nil) = %q, want Text = %q; links %#v", final, want, links)
		}
		called := 0
		formatted, _ := TextWithLinks(fragment.String(), "https://example.test", func(index int, link Link, text string) string {
			if index != called || link != links[index] || strings.ContainsRune(text, '\x00') {
				t.Fatalf("formatter(%d, %#v, %q), expected callback %d without markers", index, link, text, called)
			}
			called++
			return text
		})
		if formatted != final || called != count {
			t.Fatalf("formatted = %q, callbacks %d/%d, want %q", formatted, called, count, final)
		}
	})
}

// Deliberately simple, slow oracle for the bounded scanner on short fuzz inputs.
// Expression rendering is shared; only delimiter recognition is compared here.
func referenceLaTeXDelimiters(text string) string {
	escaped := func(pos int) bool {
		slashes := 0
		for pos > 0 && text[pos-1] == '\\' {
			pos--
			slashes++
		}
		return slashes%2 != 0
	}
	var out strings.Builder
	for pos := 0; pos < len(text); {
		converted := false
		if !escaped(pos) {
			for kind, start := range []string{"$$", `\[`, `\(`, "$"} {
				if !strings.HasPrefix(text[pos:], start) {
					continue
				}
				end := []string{"$$", `\]`, `\)`, "$"}[kind]
				close := -1
				for i := pos + len(start); i < len(text); i++ {
					if !escaped(i) && strings.HasPrefix(text[i:], end) {
						close = i
						break
					}
				}
				if close >= 0 {
					expression := text[pos+len(start) : close]
					if kind != 3 || looksLikeInlineMath(text, pos, close+1, expression) {
						rendered := strings.TrimSpace(renderLaTeXExpression(expression, kind < 2))
						if rendered == "" {
							rendered = text[pos : close+len(end)]
						}
						out.WriteString(rendered)
						pos = close + len(end)
						converted = true
					}
				}
				break
			}
		}
		if !converted {
			out.WriteByte(text[pos])
			pos++
		}
	}
	return out.String()
}
