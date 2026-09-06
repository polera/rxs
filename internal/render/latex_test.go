package render

import "testing"

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
