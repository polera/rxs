package render

import (
	"strings"
	"unicode"
)

// LaTeX replaces commonly used delimited LaTeX expressions with a compact
// Unicode representation suitable for a terminal. Text that does not look
// like delimited mathematics is returned unchanged. Supported delimiters are
// $...$, $$...$$, \(...\), and \[...\].
func LaTeX(text string) string {
	var out strings.Builder
	for pos := 0; pos < len(text); {
		start, end, display, ok := latexDelimiter(text, pos)
		if !ok {
			out.WriteByte(text[pos])
			pos++
			continue
		}
		close := findLatexClose(text, pos+len(start), end)
		if close < 0 {
			out.WriteByte(text[pos])
			pos++
			continue
		}
		expression := text[pos+len(start) : close]
		if start == "$" && !looksLikeInlineMath(text, pos, close+1, expression) {
			out.WriteByte(text[pos])
			pos++
			continue
		}
		rendered := strings.TrimSpace(renderLaTeXExpression(expression, display))
		if rendered == "" {
			out.WriteString(text[pos : close+len(end)])
		} else {
			out.WriteString(rendered)
		}
		pos = close + len(end)
	}
	return out.String()
}

func latexDelimiter(text string, pos int) (start, end string, display bool, ok bool) {
	if escapedAt(text, pos) {
		return "", "", false, false
	}
	for _, delimiter := range []struct {
		start, end string
		display    bool
	}{{"$$", "$$", true}, {`\[`, `\]`, true}, {`\(`, `\)`, false}, {"$", "$", false}} {
		if strings.HasPrefix(text[pos:], delimiter.start) {
			return delimiter.start, delimiter.end, delimiter.display, true
		}
	}
	return "", "", false, false
}

func findLatexClose(text string, from int, delimiter string) int {
	for offset := from; offset < len(text); {
		index := strings.Index(text[offset:], delimiter)
		if index < 0 {
			return -1
		}
		index += offset
		if !escapedAt(text, index) {
			return index
		}
		offset = index + len(delimiter)
	}
	return -1
}

func escapedAt(text string, pos int) bool {
	slashes := 0
	for pos > 0 && text[pos-1] == '\\' {
		slashes++
		pos--
	}
	return slashes%2 == 1
}

// Single dollars are also common currency punctuation. Require a recognizable
// math cue, or a single variable, before treating them as LaTeX delimiters.
func looksLikeInlineMath(text string, start, end int, expression string) bool {
	if expression == "" || strings.ContainsAny(expression, "\r\n") || strings.TrimSpace(expression) != expression {
		return false
	}
	if end < len(text) && text[end] >= '0' && text[end] <= '9' {
		return false
	}
	trimmed := strings.TrimSpace(expression)
	if len([]rune(trimmed)) == 1 {
		return unicode.IsLetter([]rune(trimmed)[0])
	}
	return strings.ContainsAny(trimmed, `\^_{}=+*/<>|`)
}

func renderLaTeXExpression(expression string, display bool) string {
	p := latexParser{source: expression, display: display}
	return p.sequence(0)
}

type latexParser struct {
	source  string
	pos     int
	display bool
}

func (p *latexParser) sequence(stop byte) string {
	var out strings.Builder
	for p.pos < len(p.source) {
		ch := p.source[p.pos]
		if stop != 0 && ch == stop {
			p.pos++
			break
		}
		switch ch {
		case '\\':
			out.WriteString(p.command())
		case '{':
			p.pos++
			out.WriteString(p.sequence('}'))
		case '^', '_':
			p.pos++
			value := p.argument()
			if ch == '^' {
				out.WriteString(scriptText(value, superscript, "^("))
			} else {
				out.WriteString(scriptText(value, subscript, "_("))
			}
		case '~':
			out.WriteByte(' ')
			p.pos++
		case '&':
			// Alignment markers are layout instructions, not visible math.
			p.pos++
		default:
			out.WriteByte(ch)
			p.pos++
		}
	}
	return out.String()
}

func (p *latexParser) command() string {
	p.pos++
	if p.pos >= len(p.source) {
		return `\`
	}
	if p.source[p.pos] == '\\' {
		p.pos++
		if p.display {
			return "\n"
		}
		return " "
	}
	if strings.ContainsRune(",;: ", rune(p.source[p.pos])) {
		p.pos++
		return " "
	}
	if p.source[p.pos] == '!' {
		p.pos++
		return ""
	}
	start := p.pos
	for p.pos < len(p.source) && unicode.IsLetter(rune(p.source[p.pos])) {
		p.pos++
	}
	if start == p.pos {
		literal := p.source[p.pos]
		p.pos++
		return string(literal)
	}
	name := p.source[start:p.pos]
	switch name {
	case "frac", "dfrac", "tfrac":
		numerator, denominator := p.argument(), p.argument()
		return "(" + numerator + ")/" + "(" + denominator + ")"
	case "sqrt":
		degree := ""
		p.skipSpaces()
		if p.pos < len(p.source) && p.source[p.pos] == '[' {
			p.pos++
			begin := p.pos
			for p.pos < len(p.source) && p.source[p.pos] != ']' {
				p.pos++
			}
			degree = p.source[begin:p.pos]
			if p.pos < len(p.source) {
				p.pos++
			}
		}
		value := p.argument()
		if degree == "" || degree == "2" {
			return "√(" + value + ")"
		}
		return scriptText(degree, superscript, "^(") + "√(" + value + ")"
	case "text", "textrm", "textit", "textbf", "mathrm", "mathbf", "mathit", "mathsf", "mathtt", "operatorname":
		return p.argument()
	case "left", "right":
		return ""
	case "begin", "end", "label", "tag":
		_ = p.argument()
		return ""
	case "quad", "qquad", "enspace", "space":
		return " "
	case "sin", "cos", "tan", "arcsin", "arccos", "arctan", "log", "ln", "exp", "lim", "min", "max", "det":
		return name
	}
	if symbol, ok := latexSymbols[name]; ok {
		return symbol
	}
	// Keep unsupported commands recognizable rather than silently losing data.
	return `\` + name
}

func (p *latexParser) argument() string {
	p.skipSpaces()
	if p.pos >= len(p.source) {
		return ""
	}
	if p.source[p.pos] == '{' {
		p.pos++
		return p.sequence('}')
	}
	if p.source[p.pos] == '\\' {
		return p.command()
	}
	ch := p.source[p.pos]
	p.pos++
	return string(ch)
}

func (p *latexParser) skipSpaces() {
	for p.pos < len(p.source) && unicode.IsSpace(rune(p.source[p.pos])) {
		p.pos++
	}
}

func scriptText(value string, alphabet map[rune]rune, fallback string) string {
	var out strings.Builder
	for _, ch := range value {
		replacement, ok := alphabet[ch]
		if !ok {
			return fallback + value + ")"
		}
		out.WriteRune(replacement)
	}
	return out.String()
}

var superscript = map[rune]rune{
	'0': '⁰', '1': '¹', '2': '²', '3': '³', '4': '⁴', '5': '⁵', '6': '⁶', '7': '⁷', '8': '⁸', '9': '⁹',
	'+': '⁺', '-': '⁻', '=': '⁼', '(': '⁽', ')': '⁾', 'n': 'ⁿ', 'i': 'ⁱ',
}

var subscript = map[rune]rune{
	'0': '₀', '1': '₁', '2': '₂', '3': '₃', '4': '₄', '5': '₅', '6': '₆', '7': '₇', '8': '₈', '9': '₉',
	'+': '₊', '-': '₋', '=': '₌', '(': '₍', ')': '₎', 'a': 'ₐ', 'e': 'ₑ', 'h': 'ₕ', 'i': 'ᵢ', 'j': 'ⱼ', 'k': 'ₖ', 'l': 'ₗ', 'm': 'ₘ', 'n': 'ₙ', 'o': 'ₒ', 'p': 'ₚ', 'r': 'ᵣ', 's': 'ₛ', 't': 'ₜ', 'u': 'ᵤ', 'v': 'ᵥ', 'x': 'ₓ',
}

var latexSymbols = map[string]string{
	"alpha": "α", "beta": "β", "gamma": "γ", "delta": "δ", "epsilon": "ε", "varepsilon": "ϵ", "zeta": "ζ", "eta": "η", "theta": "θ", "vartheta": "ϑ", "iota": "ι", "kappa": "κ", "lambda": "λ", "mu": "μ", "nu": "ν", "xi": "ξ", "pi": "π", "varpi": "ϖ", "rho": "ρ", "sigma": "σ", "tau": "τ", "upsilon": "υ", "phi": "φ", "varphi": "ϕ", "chi": "χ", "psi": "ψ", "omega": "ω",
	"Gamma": "Γ", "Delta": "Δ", "Theta": "Θ", "Lambda": "Λ", "Xi": "Ξ", "Pi": "Π", "Sigma": "Σ", "Upsilon": "Υ", "Phi": "Φ", "Psi": "Ψ", "Omega": "Ω",
	"times": "×", "cdot": "·", "div": "÷", "pm": "±", "mp": "∓", "le": "≤", "leq": "≤", "ge": "≥", "geq": "≥", "ne": "≠", "neq": "≠", "approx": "≈", "sim": "∼", "equiv": "≡", "propto": "∝",
	"infty": "∞", "partial": "∂", "nabla": "∇", "sum": "∑", "prod": "∏", "int": "∫", "oint": "∮", "forall": "∀", "exists": "∃", "in": "∈", "notin": "∉", "subset": "⊂", "subseteq": "⊆", "supset": "⊃", "supseteq": "⊇", "cup": "∪", "cap": "∩", "emptyset": "∅",
	"rightarrow": "→", "to": "→", "leftarrow": "←", "leftrightarrow": "↔", "Rightarrow": "⇒", "Leftarrow": "⇐", "Leftrightarrow": "⇔", "mapsto": "↦",
	"ldots": "…", "cdots": "⋯", "vdots": "⋮", "ddots": "⋱", "angle": "∠", "degree": "°", "prime": "′",
}
