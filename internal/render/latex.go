package render

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// LaTeX replaces commonly used delimited LaTeX expressions with a compact
// Unicode representation suitable for a terminal. Text that does not look
// like delimited mathematics is returned unchanged. Supported delimiters are
// $...$, $$...$$, \(...\), and \[...\].
func LaTeX(text string) string {
	return latex(text, nil)
}

func latex(text string, markers []mathMarker) string {
	if !strings.ContainsAny(text, `$\`) {
		if len(markers) == 0 {
			return text
		}
		var out strings.Builder
		writeMathRange(&out, text, 0, len(text), markers)
		return out.String()
	}
	var out strings.Builder
	var closers [4]latexCloseScanner
	written := 0
	markerAt := 0
	escaped := false
	for pos := 0; pos < len(text); {
		kind := -1
		if !escaped {
			switch {
			case strings.HasPrefix(text[pos:], "$$"):
				kind = 0
			case strings.HasPrefix(text[pos:], `\[`):
				kind = 1
			case strings.HasPrefix(text[pos:], `\(`):
				kind = 2
			case text[pos] == '$':
				kind = 3
			}
		}
		if kind < 0 || kind >= len(closers) {
			escaped = text[pos] == '\\' && !escaped
			pos++
			continue
		}
		start := [...]string{"$$", `\[`, `\(`, "$"}[kind]
		end := [...]string{"$$", `\]`, `\)`, "$"}[kind]
		// #nosec G602 -- kind is checked against len(closers) above.
		close := closers[kind].find(text, pos+len(start), end)
		if close < 0 {
			escaped = text[pos] == '\\'
			pos++
			continue
		}
		expression := text[pos+len(start) : close]
		if start == "$" && !looksLikeInlineMath(text, pos, close+1, expression) {
			pos++
			continue
		}
		first := markerAt
		for markerAt < len(markers) && markers[markerAt].pos < pos {
			markerAt++
		}
		if out.Len() == 0 {
			out.Grow(len(text))
		}
		writeMathRange(&out, text, written, pos, markers[first:markerAt])
		first = markerAt
		for markerAt < len(markers) && markers[markerAt].pos < close+len(end) {
			markerAt++
		}
		p := latexParser{source: expression, display: kind < 2, markers: markers[first:markerAt], base: pos + len(start)}
		rendered := p.sequence(0) + p.takeMarkers(len(text), false)
		plain, renderedMarkers := splitMathMarkers(rendered)
		trimmed := strings.TrimSpace(plain)
		if trimmed != "" {
			if len(renderedMarkers) != 0 {
				leading := len(plain) - len(strings.TrimLeftFunc(plain, unicode.IsSpace))
				writeMathRange(&out, plain, leading, leading+len(trimmed), renderedMarkers)
			} else {
				out.WriteString(trimmed)
			}
		} else {
			writeMathRange(&out, text, pos, close+len(end), markers[first:markerAt])
		}
		pos = close + len(end)
		written = pos
		escaped = false
	}
	if written == 0 && len(markers) == 0 {
		return text
	}
	writeMathRange(&out, text, written, len(text), markers[markerAt:])
	return out.String()
}

// Link annotations never enter the TeX grammar. Their source positions travel
// with parsed arguments, including through script and fraction transformations.
type mathMarker struct {
	pos  int
	text string
}

func splitMathMarkers(text string) (string, []mathMarker) {
	const prefix = "\x00rxs-link-"
	if !strings.Contains(text, prefix) {
		return text, nil
	}
	var out strings.Builder
	var markers []mathMarker
	for {
		start := strings.Index(text, prefix)
		if start < 0 {
			break
		}
		end := strings.IndexByte(text[start+len(prefix):], '\x00')
		if end < 0 {
			break
		}
		end += start + len(prefix) + 1
		out.WriteString(text[:start])
		markers = append(markers, mathMarker{pos: out.Len(), text: text[start:end]})
		text = text[end:]
	}
	out.WriteString(text)
	return out.String(), markers
}

func writeMathRange(out *strings.Builder, text string, from, to int, markers []mathMarker) {
	for _, marker := range markers {
		pos := max(from, min(to, marker.pos))
		out.WriteString(text[from:pos])
		out.WriteString(marker.text)
		from = pos
	}
	out.WriteString(text[from:to])
}

// A collapsed token belongs to its first contributing link. Later links remain
// empty, in source order. Close these annotations before any argument's links;
// regrouping starts and ends would turn adjacent anchors into crossing spans.
func annotateMathToken(text string, markers []mathMarker) string {
	if len(markers) == 0 {
		return text
	}
	var out strings.Builder
	for len(markers) > 0 && strings.HasSuffix(markers[0].text, "-start\x00") {
		out.WriteString(markers[0].text)
		markers = markers[1:]
	}
	end := strings.Index(text, "\x00rxs-link-")
	if end < 0 {
		end = len(text)
	}
	out.WriteString(text[:end])
	for _, marker := range markers {
		out.WriteString(marker.text)
	}
	out.WriteString(text[end:])
	return out.String()
}

// Each delimiter kind advances its own cursor, including after failed searches.
// Thus unmatched openers and escape runs cost at most four closer-search passes.
type latexCloseScanner struct {
	pos     int
	escaped bool
}

func (s *latexCloseScanner) find(text string, from int, delimiter string) int {
	for s.pos < len(text) {
		if s.pos >= from && !s.escaped && strings.HasPrefix(text[s.pos:], delimiter) {
			return s.pos
		}
		s.escaped = text[s.pos] == '\\' && !s.escaped
		s.pos++
	}
	return -1
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
	ch, size := utf8.DecodeRuneInString(expression)
	if size == len(expression) {
		return unicode.IsLetter(ch)
	}
	return strings.ContainsAny(expression, `\^_{}=+*/<>|`)
}

func renderLaTeXExpression(expression string, display bool) string {
	p := latexParser{source: expression, display: display}
	return p.sequence(0)
}

// renderDirectMath is for HTML elements already identified as math. Unlike
// LaTeX, it intentionally accepts an undelimited TeX fallback.
func renderDirectMath(fallback string, display bool) string {
	plain, markers := splitMathMarkers(fallback)
	from, to, delimiterDisplay, delimited := mathFallbackDelimiter(plain)
	if !delimited {
		from = len(plain) - len(strings.TrimLeftFunc(plain, unicode.IsSpace))
		to = len(strings.TrimRightFunc(plain, unicode.IsSpace))
		if from > to {
			from = to
		}
	}
	for i := range markers {
		markers[i].pos = max(0, min(to, markers[i].pos)-from)
	}
	p := latexParser{source: plain[from:to], display: display || delimiterDisplay, markers: markers}
	rendered := p.sequence(0) + p.takeMarkers(len(plain), false)
	text, renderedMarkers := splitMathMarkers(rendered)
	trimmed := strings.TrimSpace(text)
	if len(renderedMarkers) == 0 {
		return trimmed
	}
	leading := len(text) - len(strings.TrimLeftFunc(text, unicode.IsSpace))
	var out strings.Builder
	writeMathRange(&out, text, leading, leading+len(trimmed), renderedMarkers)
	return out.String()
}

// mathFallbackDelimiter recognizes only a complete, trimmed math wrapper.
func mathFallbackDelimiter(text string) (from, to int, display, ok bool) {
	from = len(text) - len(strings.TrimLeftFunc(text, unicode.IsSpace))
	to = len(strings.TrimRightFunc(text, unicode.IsSpace))
	if from > to {
		return 0, 0, false, false
	}
	trimmed := text[from:to]
	for _, delimiter := range []struct {
		open, close string
		display     bool
	}{{`\[`, `\]`, true}, {"$$", "$$", true}, {`\(`, `\)`, false}, {"$", "$", false}} {
		if len(trimmed) >= len(delimiter.open)+len(delimiter.close) && strings.HasPrefix(trimmed, delimiter.open) && strings.HasSuffix(trimmed, delimiter.close) {
			return from + len(delimiter.open), to - len(delimiter.close), delimiter.display, true
		}
	}
	return 0, 0, false, false
}

type latexParser struct {
	source       string
	pos          int
	display      bool
	depth        int
	markers      []mathMarker
	base         int
	environments [128]string
	environment  int
}

func (p *latexParser) takeMarkers(pos int, endsOnly bool) string {
	var out strings.Builder
	for len(p.markers) > 0 {
		marker := p.markers[0]
		if marker.pos > pos || marker.pos == pos && endsOnly && strings.HasSuffix(marker.text, "-start\x00") {
			break
		}
		out.WriteString(marker.text)
		p.markers = p.markers[1:]
	}
	return out.String()
}

func (p *latexParser) sequence(stop byte) string {
	if p.depth >= 128 {
		rest := p.source[p.pos:]
		p.pos = len(p.source)
		return rest
	}
	p.depth++
	defer func() { p.depth-- }()
	var out strings.Builder
	for p.pos < len(p.source) {
		out.WriteString(p.takeMarkers(p.base+p.pos, false))
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
			if p.environment > 0 && p.environments[p.environment-1] == "cases" {
				spaces := 0
				if p.pos > 0 && unicode.IsSpace(rune(p.source[p.pos-1])) {
					spaces++
				}
				if p.pos+1 < len(p.source) && unicode.IsSpace(rune(p.source[p.pos+1])) {
					spaces++
				}
				out.WriteString(strings.Repeat(" ", 2-spaces))
			}
			// Other alignment markers are layout instructions, not visible math.
			p.pos++
		default:
			out.WriteByte(ch)
			p.pos++
		}
	}
	out.WriteString(p.takeMarkers(p.base+p.pos, true))
	return out.String()
}

func (p *latexParser) command() (result string) {
	p.pos++
	name := ""
	switch {
	case p.pos >= len(p.source):
		result = `\`
	case p.source[p.pos] == '\\':
		p.pos++
		result = " "
		if p.display {
			result = "\n"
		}
	case strings.ContainsRune(",;: ", rune(p.source[p.pos])):
		p.pos++
		result = " "
	case p.source[p.pos] == '!':
		p.pos++
	default:
		start := p.pos
		for p.pos < len(p.source) && (p.source[p.pos] >= 'a' && p.source[p.pos] <= 'z' || p.source[p.pos] >= 'A' && p.source[p.pos] <= 'Z') {
			p.pos++
		}
		name = p.source[start:p.pos]
		if name == "" {
			literal, size := utf8.DecodeRuneInString(p.source[p.pos:])
			p.pos += size
			result = string(literal)
			if unicode.IsLetter(literal) {
				result = `\` + result
			}
		}
	}
	count := 0
	for count < len(p.markers) && (p.markers[count].pos < p.base+p.pos || p.markers[count].pos == p.base+p.pos && strings.HasSuffix(p.markers[count].text, "-end\x00")) {
		count++
	}
	markers := p.markers[:count]
	p.markers = p.markers[count:]
	defer func() { result = annotateMathToken(result, markers) }()
	if name == "" {
		return result
	}
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
			var annotated strings.Builder
			for len(p.markers) > 0 && p.markers[0].pos <= p.base+p.pos {
				marker := p.markers[0]
				pos := max(begin, min(p.pos, marker.pos-p.base))
				annotated.WriteString(p.source[begin:pos])
				annotated.WriteString(marker.text)
				begin = pos
				p.markers = p.markers[1:]
			}
			annotated.WriteString(p.source[begin:p.pos])
			degree = annotated.String()
			if p.pos < len(p.source) {
				p.pos++
			}
		}
		value := p.argument()
		plainDegree, degreeMarkers := splitMathMarkers(degree)
		if plainDegree == "" || plainDegree == "2" {
			// The implicit square-root degree has no glyph of its own. Keep
			// any annotation on the root symbol rather than restoring "2".
			return annotateMathToken("√", degreeMarkers) + "(" + value + ")"
		}
		return scriptText(degree, superscript, "^(") + "√(" + value + ")"
	case "text", "textrm", "textit", "textbf", "mathrm", "mathbf", "mathit", "mathsf", "mathtt", "operatorname":
		return p.argument()
	case "vec":
		return decorateMath(p.argument(), "\u20d7")
	case "hat", "widehat":
		return decorateMath(p.argument(), "\u0302")
	case "mathbb":
		return mapMathStyle(p.argument(), blackboardBold)
	case "mathcal":
		return mapMathStyle(p.argument(), calligraphic)
	case "boxed":
		return "[" + p.argument() + "]"
	case "big", "Big", "bigg", "Bigg", "bigl", "bigr", "Bigl", "Bigr", "biggl", "biggr", "Biggl", "Biggr":
		return ""
	case "langle":
		p.skipSpaces()
		return "⟨"
	case "left", "right":
		p.skipSpaces()
		if p.pos < len(p.source) && p.source[p.pos] == '.' {
			p.pos++
			return p.takeMarkers(p.base+p.pos, false)
		}
		return ""
	case "begin", "end":
		environment, markers := splitMathMarkers(p.argument())
		if name == "begin" {
			if p.environment < len(p.environments) {
				p.environments[p.environment] = environment
				p.environment++
			}
		} else if p.environment > 0 && p.environments[p.environment-1] == environment {
			p.environment--
			p.environments[p.environment] = ""
		}
		var out strings.Builder
		writeMathRange(&out, "", 0, 0, markers)
		return out.String()
	case "label", "tag":
		_, markers := splitMathMarkers(p.argument())
		var out strings.Builder
		writeMathRange(&out, "", 0, 0, markers)
		return out.String()
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

func (p *latexParser) argument() (result string) {
	// Bound recursive command arguments as well as nested brace groups.
	if p.depth >= 128 {
		rest := p.source[p.pos:]
		p.pos = len(p.source)
		return rest
	}
	p.depth++
	defer func() { p.depth-- }()
	p.skipSpaces()
	opening := p.takeMarkers(p.base+p.pos, false)
	defer func() { result = opening + result + p.takeMarkers(p.base+p.pos, true) }()
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
	ch, size := utf8.DecodeRuneInString(p.source[p.pos:])
	p.pos += size
	return string(ch)
}

func (p *latexParser) skipSpaces() {
	for p.pos < len(p.source) {
		ch, size := utf8.DecodeRuneInString(p.source[p.pos:])
		if !unicode.IsSpace(ch) {
			break
		}
		p.pos += size
	}
}

func scriptText(value string, alphabet map[rune]rune, fallback string) string {
	plain, markers := splitMathMarkers(value)
	var out strings.Builder
	markerAt := 0
	for pos, ch := range plain {
		for markerAt < len(markers) && markers[markerAt].pos <= pos {
			out.WriteString(markers[markerAt].text)
			markerAt++
		}
		replacement, ok := alphabet[ch]
		if !ok {
			return fallback + value + ")"
		}
		out.WriteRune(replacement)
	}
	for _, marker := range markers[markerAt:] {
		out.WriteString(marker.text)
	}
	return out.String()
}

func decorateMath(value, suffix string) string {
	plain, markers := splitMathMarkers(value)
	insert := len(plain)
	foundBase := false
	for pos, ch := range plain {
		if !foundBase {
			if unicode.IsSpace(ch) {
				continue
			}
			foundBase = true
			insert = pos + utf8.RuneLen(ch)
			continue
		}
		if pos != insert || !unicode.Is(unicode.M, ch) {
			break
		}
		insert = pos + utf8.RuneLen(ch)
	}
	var out strings.Builder
	markerAt := 0
	from := 0
	for markerAt < len(markers) && markers[markerAt].pos < insert {
		pos := max(from, min(insert, markers[markerAt].pos))
		out.WriteString(plain[from:pos])
		out.WriteString(markers[markerAt].text)
		from = pos
		markerAt++
	}
	out.WriteString(plain[from:insert])
	out.WriteString(suffix)
	writeMathRange(&out, plain, insert, len(plain), markers[markerAt:])
	return out.String()
}

func mapMathStyle(value string, alphabet map[rune]rune) string {
	plain, markers := splitMathMarkers(value)
	var out strings.Builder
	markerAt := 0
	for pos, ch := range plain {
		for markerAt < len(markers) && markers[markerAt].pos <= pos {
			out.WriteString(markers[markerAt].text)
			markerAt++
		}
		if replacement, ok := alphabet[ch]; ok {
			out.WriteRune(replacement)
		} else {
			out.WriteRune(ch)
		}
	}
	for _, marker := range markers[markerAt:] {
		out.WriteString(marker.text)
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

var blackboardBold = map[rune]rune{
	'C': 'ℂ', 'H': 'ℍ', 'N': 'ℕ', 'P': 'ℙ', 'Q': 'ℚ', 'R': 'ℝ', 'Z': 'ℤ',
}

var calligraphic = map[rune]rune{
	'B': 'ℬ', 'E': 'ℰ', 'F': 'ℱ', 'H': 'ℋ', 'I': 'ℐ', 'L': 'ℒ', 'M': 'ℳ', 'R': 'ℛ',
}

var latexSymbols = map[string]string{
	"alpha": "α", "beta": "β", "gamma": "γ", "delta": "δ", "epsilon": "ε", "varepsilon": "ϵ", "zeta": "ζ", "eta": "η", "theta": "θ", "vartheta": "ϑ", "iota": "ι", "kappa": "κ", "lambda": "λ", "mu": "μ", "nu": "ν", "xi": "ξ", "pi": "π", "varpi": "ϖ", "rho": "ρ", "sigma": "σ", "tau": "τ", "upsilon": "υ", "phi": "φ", "varphi": "ϕ", "chi": "χ", "psi": "ψ", "omega": "ω",
	"Gamma": "Γ", "Delta": "Δ", "Theta": "Θ", "Lambda": "Λ", "Xi": "Ξ", "Pi": "Π", "Sigma": "Σ", "Upsilon": "Υ", "Phi": "Φ", "Psi": "Ψ", "Omega": "Ω",
	"times": "×", "cdot": "·", "div": "÷", "pm": "±", "mp": "∓", "le": "≤", "leq": "≤", "ge": "≥", "geq": "≥", "ne": "≠", "neq": "≠", "approx": "≈", "sim": "∼", "equiv": "≡", "propto": "∝",
	"infty": "∞", "partial": "∂", "nabla": "∇", "sum": "∑", "prod": "∏", "int": "∫", "oint": "∮", "forall": "∀", "exists": "∃", "in": "∈", "notin": "∉", "subset": "⊂", "subseteq": "⊆", "supset": "⊃", "supseteq": "⊇", "cup": "∪", "cap": "∩", "emptyset": "∅",
	"rightarrow": "→", "to": "→", "leftarrow": "←", "leftrightarrow": "↔", "Rightarrow": "⇒", "Leftarrow": "⇐", "Leftrightarrow": "⇔", "mapsto": "↦",
	"ldots": "…", "dots": "…", "cdots": "⋯", "vdots": "⋮", "ddots": "⋱", "angle": "∠", "langle": "⟨", "rangle": "⟩", "degree": "°", "prime": "′",
	"ast": "∗", "implies": "⇒", "blacksquare": "■",
}
