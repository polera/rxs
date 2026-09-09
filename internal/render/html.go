// Package render converts downloaded HTML into readable terminal text.
package render

import (
	"fmt"
	"html"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Link is a navigable HTTP(S) hyperlink found in an article.
type Link struct {
	Text string
	URL  string
}

// LinkFormatter renders the visible text for a hyperlink. The index matches
// the link's position in the slice returned by TextWithLinks.
type LinkFormatter func(index int, link Link, text string) string

// Text extracts human-readable text while retaining useful block boundaries.
func Text(fragment string) string {
	if strings.TrimSpace(fragment) == "" {
		return ""
	}
	nodes, err := parseFragment(fragment)
	if err != nil {
		return strings.TrimSpace(html.UnescapeString(stripTags(fragment)))
	}
	renderer := newTextRenderer(nil, nil, nil, headingBase(nodes))
	return renderer.render(nodes)
}

// TextWithLinks extracts readable text and preserves valid HTTP(S) anchors in
// place. The formatter can add terminal hyperlink/style sequences around the
// anchor's visible words; when it is nil, plain text is returned.
func TextWithLinks(fragment, baseURL string, formatter LinkFormatter) (string, []Link) {
	if strings.TrimSpace(fragment) == "" {
		return "", nil
	}
	nodes, err := parseFragment(fragment)
	if err != nil {
		return strings.TrimSpace(html.UnescapeString(stripTags(fragment))), nil
	}
	base, _ := url.Parse(strings.TrimSpace(baseURL))
	var links []Link
	var fallbacks []bool
	renderer := newTextRenderer(base, &links, &fallbacks, headingBase(nodes))
	content := renderer.render(nodes)
	return formatLinkMarkers(content, links, fallbacks, formatter), links
}

func parseFragment(fragment string) ([]*xhtml.Node, error) {
	nodes, err := xhtml.ParseFragment(strings.NewReader(fragment), &xhtml.Node{Type: xhtml.ElementNode, Data: "div", DataAtom: atom.Div})
	if err != nil || hasHTMLElement(nodes) {
		return nodes, err
	}

	// Some feeds put plain text in fields normally used for HTML. Treat it as
	// preformatted content so line breaks and angle-bracket notation survive.
	pre := &xhtml.Node{Type: xhtml.ElementNode, Data: "pre", DataAtom: atom.Pre}
	pre.AppendChild(&xhtml.Node{Type: xhtml.TextNode, Data: fragment})
	return []*xhtml.Node{pre}, nil
}

func hasHTMLElement(nodes []*xhtml.Node) bool {
	var visit func(*xhtml.Node) bool
	visit = func(node *xhtml.Node) bool {
		if node.Type == xhtml.ElementNode && node.DataAtom != 0 {
			return true
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if visit(child) {
				return true
			}
		}
		return false
	}
	for _, node := range nodes {
		if visit(node) {
			return true
		}
	}
	return false
}

// Links extracts hyperlinks in document order. Relative URLs are resolved
// against baseURL; unsupported schemes are omitted because the application
// deliberately only hands HTTP(S) URLs to browsers.
func Links(fragment, baseURL string) []Link {
	_, links := TextWithLinks(fragment, baseURL, nil)
	return links
}

type textRenderer struct {
	b            strings.Builder
	base         *url.URL
	links        *[]Link
	fallbacks    *[]bool
	headingBase  int
	listDepth    int
	pendingSpace bool
	mathStart    int
	mathRegions  [][2]int
	disableMath  bool
}

func newTextRenderer(base *url.URL, links *[]Link, fallbacks *[]bool, baseHeading int) *textRenderer {
	return &textRenderer{base: base, links: links, fallbacks: fallbacks, headingBase: baseHeading}
}

func (r *textRenderer) render(nodes []*xhtml.Node) string {
	for _, node := range nodes {
		r.renderNode(node)
	}
	r.endMathRegion()
	r.pendingSpace = false
	content := r.b.String()
	if !r.disableMath && len(r.mathRegions) > 0 {
		var out strings.Builder
		out.Grow(len(content))
		pos := 0
		for _, region := range r.mathRegions {
			out.WriteString(content[pos:region[0]])
			text, markers := splitMathMarkers(content[region[0]:region[1]])
			out.WriteString(latex(text, markers))
			pos = region[1]
		}
		out.WriteString(content[pos:])
		content = out.String()
	}
	return strings.Trim(strings.TrimRight(content, " \t"), "\n")
}

// Inline HTML (including links and br) shares a prose region. Block boundaries and
// literal/already-rendered output end it, so code and subtrees are never parsed
// as math and delimiters cannot pair across separate blocks or table cells.
func (r *textRenderer) endMathRegion() {
	end := r.b.Len()
	if strings.ContainsAny(r.b.String()[r.mathStart:end], `$\`) {
		r.mathRegions = append(r.mathRegions, [2]int{r.mathStart, end})
	}
	r.mathStart = end
}

func (r *textRenderer) renderNode(node *xhtml.Node) {
	if node.Type == xhtml.TextNode {
		r.writeText(node.Data)
		return
	}
	if node.Type != xhtml.ElementNode {
		return
	}
	if node.Data == "script" && isLaTeXScript(node) {
		r.renderLaTeXScript(node)
		return
	}
	if isHiddenElement(node.Data) {
		return
	}

	switch node.Data {
	case "a":
		r.renderLink(node)
	case "br":
		r.pendingSpace = false
		if r.b.Len() > 0 && !strings.HasSuffix(r.b.String(), "\n") {
			r.b.WriteByte('\n')
		}
	case "hr":
		r.blockBreak()
		r.writeLiteral("────────")
		r.blockBreak()
	case "p", "div", "article", "section", "header", "footer", "main", "aside", "figure", "figcaption":
		r.renderBlock(node)
	case "h1", "h2", "h3", "h4", "h5", "h6":
		r.renderHeading(node)
	case "ul":
		r.renderList(node, false)
	case "ol":
		r.renderList(node, true)
	case "li":
		r.renderLooseListItem(node)
	case "blockquote":
		r.renderBlockquote(node)
	case "pre":
		r.renderPre(node)
	case "code":
		r.renderInlineCode(node)
	case "table":
		r.renderTable(node)
	case "img":
		r.renderImage(node)
	case "object":
		r.renderObject(node)
	default:
		r.renderChildren(node)
	}
}

func (r *textRenderer) renderChildren(node *xhtml.Node) {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		r.renderNode(child)
	}
}

func (r *textRenderer) renderBlock(node *xhtml.Node) {
	r.blockBreak()
	r.renderChildren(node)
	r.blockBreak()
}

func (r *textRenderer) renderHeading(node *xhtml.Node) {
	level, _ := strconv.Atoi(strings.TrimPrefix(node.Data, "h"))
	level = min(6, max(2, level-r.headingBase+2))
	r.blockBreak()
	r.writeLiteral(strings.Repeat("#", level) + " ")
	r.renderChildren(node)
	r.blockBreak()
}

func (r *textRenderer) renderList(node *xhtml.Node, ordered bool) {
	nested := r.listDepth > 0
	if nested {
		r.lineBreak()
	} else {
		r.blockBreak()
	}
	r.listDepth++
	number := listStart(node)
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type != xhtml.ElementNode || child.Data != "li" {
			continue
		}
		marker := "• "
		if ordered {
			marker = strconv.Itoa(number) + ". "
			number++
		}
		r.renderListItem(child, marker)
	}
	r.listDepth--
	if nested {
		r.lineBreak()
	} else {
		r.blockBreak()
	}
}

func listStart(node *xhtml.Node) int {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, "start") {
			if start, err := strconv.Atoi(strings.TrimSpace(attr.Val)); err == nil {
				return start
			}
		}
	}
	return 1
}

func (r *textRenderer) renderListItem(node *xhtml.Node, marker string) {
	r.lineBreak()
	r.writeLiteral(strings.Repeat("  ", max(0, r.listDepth-1)) + marker)
	paragraphs := 0
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == xhtml.ElementNode && child.Data == "p" {
			if paragraphs > 0 {
				r.blockBreak()
				r.writeLiteral(strings.Repeat("  ", r.listDepth))
			}
			r.renderChildren(child)
			paragraphs++
			continue
		}
		r.renderNode(child)
	}
	r.lineBreak()
}

func (r *textRenderer) renderLooseListItem(node *xhtml.Node) {
	r.blockBreak()
	r.writeLiteral("• ")
	r.renderChildren(node)
	r.blockBreak()
}

func (r *textRenderer) renderBlockquote(node *xhtml.Node) {
	content := r.renderSubtree(node)
	if content == "" {
		return
	}
	r.blockBreak()
	for index, line := range strings.Split(content, "\n") {
		if index > 0 {
			r.lineBreak()
		}
		r.writeLiteral("│")
		if line != "" {
			r.writeLiteral(" " + line)
		}
	}
	r.blockBreak()
}

func (r *textRenderer) renderPre(node *xhtml.Node) {
	content := strings.ReplaceAll(rawNodeText(node), "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	content = strings.Trim(content, "\n")
	if content == "" {
		return
	}
	r.blockBreak()
	for index, line := range strings.Split(content, "\n") {
		if index > 0 {
			r.lineBreak()
		}
		r.writeLiteral("    " + line)
	}
	r.blockBreak()
}

func (r *textRenderer) renderInlineCode(node *xhtml.Node) {
	content := strings.Join(strings.Fields(rawNodeText(node)), " ")
	if content != "" {
		r.writeLiteral("`" + content + "`")
	}
}

func (r *textRenderer) renderLaTeXScript(node *xhtml.Node) {
	var source strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == xhtml.TextNode {
			source.WriteString(child.Data)
		}
	}
	content := strings.TrimSpace(source.String())
	if content == "" {
		return
	}
	display := strings.Contains(strings.ToLower(nodeAttribute(node, "type")), "mode=display") ||
		strings.EqualFold(nodeAttribute(node, "mode"), "display")
	if display {
		r.blockBreak()
	}
	r.writeLiteral(renderLaTeXExpression(content, display))
	if display {
		r.blockBreak()
	}
}

func isLaTeXScript(node *xhtml.Node) bool {
	kind := strings.ToLower(nodeAttribute(node, "type"))
	return strings.HasPrefix(kind, "math/tex") || strings.HasPrefix(kind, "application/x-tex")
}

func (r *textRenderer) renderTable(node *xhtml.Node) {
	rows := tableRows(node)
	if len(rows) == 0 {
		return
	}
	r.blockBreak()

	firstCells := tableCells(rows[0])
	hasHeaders := len(firstCells) > 0
	for _, cell := range firstCells {
		hasHeaders = hasHeaders && cell.Data == "th"
	}
	if hasHeaders && len(rows) > 1 {
		headings := make([]string, len(firstCells))
		for index, cell := range firstCells {
			headings[index] = r.tableCellText(cell, false)
			if headings[index] == "" {
				headings[index] = fmt.Sprintf("Column %d", index+1)
			}
		}
		for rowIndex, row := range rows[1:] {
			if rowIndex > 0 {
				r.blockBreak()
			}
			for index, cell := range tableCells(row) {
				label := fmt.Sprintf("Column %d", index+1)
				if index < len(headings) {
					label = headings[index]
				}
				r.writeLiteral(label + ": ")
				r.writeLiteral(r.tableCellText(cell, true))
				r.lineBreak()
			}
		}
		r.blockBreak()
		return
	}

	for index, row := range rows {
		if index > 0 {
			r.lineBreak()
		}
		values := make([]string, 0, len(tableCells(row)))
		for _, cell := range tableCells(row) {
			values = append(values, r.tableCellText(cell, true))
		}
		if len(values) > 0 {
			r.writeLiteral("| " + strings.Join(values, " | ") + " |")
		}
	}
	r.blockBreak()
}

func (r *textRenderer) tableCellText(node *xhtml.Node, preserveLinks bool) string {
	links := r.links
	if !preserveLinks {
		links = nil
	}
	sub := newTextRenderer(r.base, links, r.fallbacks, r.headingBase)
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		sub.renderNode(child)
	}
	return strings.Join(strings.Fields(sub.render(nil)), " ")
}

func (r *textRenderer) renderImage(node *xhtml.Node) {
	alt := nodeAttribute(node, "alt")
	_, _, delimiterDisplay, delimited := mathFallbackDelimiter(alt)
	if (isMathAsset(node, "src") || delimited) && alt != "" {
		display := hasClass(node, "align-center") || delimiterDisplay
		if display {
			r.blockBreak()
		}
		r.writeLiteral(renderDirectMath(alt, display))
		if display {
			r.blockBreak()
		}
		return
	}
	if alt == "" {
		alt = nodeAttribute(node, "title")
	}
	if alt == "" {
		r.writeText("[Image]")
		return
	}
	r.writeText("[Image: " + strings.Join(strings.Fields(alt), " ") + "]")
}

func (r *textRenderer) renderObject(node *xhtml.Node) {
	_, _, delimiterDisplay, delimited := mathFallbackDelimiter(rawNodeText(node))
	if !isMathAsset(node, "data") && !delimited {
		r.renderChildren(node)
		return
	}
	fallback := r.renderRawSubtree(node)
	display := hasClass(node, "align-center") || delimiterDisplay
	if display {
		r.blockBreak()
	}
	r.writeLiteral(renderDirectMath(fallback, display))
	if display {
		r.blockBreak()
	}
}

func (r *textRenderer) renderRawSubtree(node *xhtml.Node) string {
	sub := newTextRenderer(r.base, r.links, r.fallbacks, r.headingBase)
	sub.disableMath = true
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		sub.renderNode(child)
	}
	return sub.render(nil)
}

func isMathAsset(node *xhtml.Node, sourceAttribute string) bool {
	return hasClass(node, "latex-math") || strings.Contains(strings.ToLower(nodeAttribute(node, sourceAttribute)), "/images/math/")
}

func hasClass(node *xhtml.Node, want string) bool {
	for _, class := range strings.Fields(nodeAttribute(node, "class")) {
		if strings.EqualFold(class, want) {
			return true
		}
	}
	return false
}

func (r *textRenderer) renderLink(node *xhtml.Node) {
	target, ok := nodeHTTPURL(node, r.base)
	if !ok || r.links == nil {
		r.renderChildren(node)
		return
	}
	label := strings.Join(strings.Fields(visibleNodeText(node)), " ")
	// Only source-empty anchors need fallback text. Math can legitimately erase
	// a nonempty label; restoring that label would undo the conversion.
	*r.fallbacks = append(*r.fallbacks, label == "")
	if label == "" {
		label = target
	}
	index := len(*r.links)
	*r.links = append(*r.links, Link{Text: label, URL: target})
	r.flushSpace()
	r.b.WriteString(linkMarker(index, "start"))
	r.renderChildren(node)
	r.flushSpace()
	r.b.WriteString(linkMarker(index, "end"))
}

func (r *textRenderer) renderSubtree(node *xhtml.Node) string {
	sub := newTextRenderer(r.base, r.links, r.fallbacks, r.headingBase)
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		sub.renderNode(child)
	}
	return sub.render(nil)
}

func (r *textRenderer) writeText(value string) {
	value = strings.ReplaceAll(value, "\u00a0", " ")
	fields := strings.Fields(value)
	if len(fields) == 0 {
		if value != "" && r.b.Len() > 0 && !strings.HasSuffix(r.b.String(), "\n") {
			r.pendingSpace = true
		}
		return
	}
	runes := []rune(value)
	leading := unicode.IsSpace(runes[0])
	trailing := unicode.IsSpace(runes[len(runes)-1])
	if leading || r.pendingSpace {
		r.pendingSpace = true
		r.flushSpace()
	}
	r.b.WriteString(strings.Join(fields, " "))
	r.pendingSpace = trailing
}

func (r *textRenderer) writeLiteral(value string) {
	r.endMathRegion()
	r.flushSpace()
	r.b.WriteString(value)
	r.mathStart = r.b.Len()
}

func (r *textRenderer) flushSpace() {
	if r.pendingSpace && r.b.Len() > 0 && !strings.HasSuffix(r.b.String(), "\n") && !strings.HasSuffix(r.b.String(), " ") {
		r.b.WriteByte(' ')
	}
	r.pendingSpace = false
}

func (r *textRenderer) lineBreak() {
	r.endMathRegion()
	r.pendingSpace = false
	if r.b.Len() > 0 && !strings.HasSuffix(r.b.String(), "\n") {
		r.b.WriteByte('\n')
	}
	r.mathStart = r.b.Len()
}

func (r *textRenderer) blockBreak() {
	r.endMathRegion()
	r.pendingSpace = false
	if r.b.Len() == 0 {
		return
	}
	trailing := 0
	for index := len(r.b.String()) - 1; index >= 0 && r.b.String()[index] == '\n'; index-- {
		trailing++
	}
	for trailing < 2 {
		r.b.WriteByte('\n')
		trailing++
	}
	r.mathStart = r.b.Len()
}

func headingBase(nodes []*xhtml.Node) int {
	base := 7
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode && len(node.Data) == 2 && node.Data[0] == 'h' && node.Data[1] >= '1' && node.Data[1] <= '6' {
			base = min(base, int(node.Data[1]-'0'))
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	for _, node := range nodes {
		walk(node)
	}
	if base == 7 {
		return 1
	}
	return base
}

func tableRows(table *xhtml.Node) []*xhtml.Node {
	var rows []*xhtml.Node
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node != table && node.Type == xhtml.ElementNode && node.Data == "table" {
			return
		}
		if node.Type == xhtml.ElementNode && node.Data == "tr" {
			rows = append(rows, node)
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(table)
	return rows
}

func tableCells(row *xhtml.Node) []*xhtml.Node {
	var cells []*xhtml.Node
	for child := row.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == xhtml.ElementNode && (child.Data == "th" || child.Data == "td") {
			cells = append(cells, child)
		}
	}
	return cells
}

func rawNodeText(node *xhtml.Node) string {
	var b strings.Builder
	var walk func(*xhtml.Node)
	walk = func(current *xhtml.Node) {
		if current.Type == xhtml.ElementNode && isHiddenElement(current.Data) {
			return
		}
		if current.Type == xhtml.TextNode {
			b.WriteString(current.Data)
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return b.String()
}

func nodeAttribute(node *xhtml.Node, name string) string {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, name) {
			return strings.TrimSpace(attr.Val)
		}
	}
	return ""
}

func nodeHTTPURL(node *xhtml.Node, base *url.URL) (string, bool) {
	return resolveHTTPURL(nodeAttribute(node, "href"), base)
}

func linkMarker(index int, edge string) string {
	return fmt.Sprintf("\x00rxs-link-%d-%s\x00", index, edge)
}

func formatLinkMarkers(content string, links []Link, fallbacks []bool, formatter LinkFormatter) string {
	if len(links) == 0 {
		return content
	}
	var out strings.Builder
	out.Grow(len(content))
	var suffixes []string
	for index, link := range links {
		start := linkMarker(index, "start")
		end := linkMarker(index, "end")
		startAt := strings.Index(content, start)
		for startAt < 0 && len(suffixes) > 0 {
			out.WriteString(content)
			content = suffixes[len(suffixes)-1]
			suffixes = suffixes[:len(suffixes)-1]
			startAt = strings.Index(content, start)
		}
		if startAt < 0 {
			continue
		}
		visibleStart := startAt + len(start)
		endOffset := strings.Index(content[visibleStart:], end)
		if endOffset < 0 {
			continue
		}
		endAt := visibleStart + endOffset
		visible := content[visibleStart:endAt]
		leading := len(visible) - len(strings.TrimLeftFunc(visible, unicode.IsSpace))
		trailing := len(visible) - len(strings.TrimRightFunc(visible, unicode.IsSpace))
		coreEnd := len(visible) - trailing
		if coreEnd < leading {
			coreEnd = leading
		}
		core := visible[leading:coreEnd]
		if core == "" && index < len(fallbacks) && fallbacks[index] {
			core = link.Text
		}
		rendered := core
		if formatter != nil {
			rendered = formatter(index, link, core)
		}
		out.WriteString(content[:startAt])
		out.WriteString(visible[:leading])
		if strings.Contains(rendered, "\x00rxs-link-") {
			// Malformed HTML can retain nested anchors across table cells.
			// Visit their rendered label before resuming the untouched suffix;
			// only link labels, never the whole document, are revisited.
			suffixes = append(suffixes, content[endAt+len(end):], visible[coreEnd:])
			content = rendered
			continue
		}
		out.WriteString(rendered)
		out.WriteString(visible[coreEnd:])
		content = content[endAt+len(end):]
	}
	out.WriteString(content)
	for i := len(suffixes) - 1; i >= 0; i-- {
		out.WriteString(suffixes[i])
	}
	return out.String()
}

func resolveHTTPURL(href string, base *url.URL) (string, bool) {
	if href == "" {
		return "", false
	}
	target, err := url.Parse(href)
	if err != nil {
		return "", false
	}
	if !target.IsAbs() {
		if base == nil || !base.IsAbs() {
			return "", false
		}
		target = base.ResolveReference(target)
	}
	target.Scheme = strings.ToLower(target.Scheme)
	if (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
		return "", false
	}
	return target.String(), true
}

func visibleNodeText(node *xhtml.Node) string {
	var b strings.Builder
	var walk func(*xhtml.Node)
	walk = func(current *xhtml.Node) {
		if current.Type == xhtml.ElementNode && isHiddenElement(current.Data) {
			return
		}
		if current.Type == xhtml.ElementNode && current.Data == "img" {
			b.WriteString(nodeAttribute(current, "alt"))
		}
		if current.Type == xhtml.TextNode {
			b.WriteString(current.Data)
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return b.String()
}

func isHiddenElement(name string) bool {
	switch name {
	case "script", "style", "noscript", "svg", "template":
		return true
	default:
		return false
	}
}

func stripTags(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch r {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}
