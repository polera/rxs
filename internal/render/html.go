// Package render converts downloaded HTML into readable terminal text.
package render

import (
	"html"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

var whitespace = regexp.MustCompile(`[ \t\r\f\v]+`)

// Link is a navigable HTTP(S) hyperlink found in an article.
type Link struct {
	Text string
	URL  string
}

// Text extracts human-readable text while retaining useful block boundaries.
func Text(fragment string) string {
	if strings.TrimSpace(fragment) == "" {
		return ""
	}
	nodes, err := xhtml.ParseFragment(strings.NewReader(fragment), &xhtml.Node{Type: xhtml.ElementNode, Data: "div", DataAtom: atom.Div})
	if err != nil {
		return strings.TrimSpace(html.UnescapeString(stripTags(fragment)))
	}
	var b strings.Builder
	for _, node := range nodes {
		writeNode(&b, node)
	}
	lines := strings.Split(strings.ReplaceAll(b.String(), "\u00a0", " "), "\n")
	cleaned := make([]string, 0, len(lines))
	blank := true
	for _, line := range lines {
		line = strings.TrimSpace(whitespace.ReplaceAllString(line, " "))
		if line == "" {
			if !blank {
				cleaned = append(cleaned, "")
				blank = true
			}
			continue
		}
		cleaned = append(cleaned, line)
		blank = false
	}
	for len(cleaned) > 0 && cleaned[len(cleaned)-1] == "" {
		cleaned = cleaned[:len(cleaned)-1]
	}
	return strings.Join(cleaned, "\n")
}

// Links extracts hyperlinks in document order. Relative URLs are resolved
// against baseURL; unsupported schemes are omitted because the application
// deliberately only hands HTTP(S) URLs to browsers.
func Links(fragment, baseURL string) []Link {
	if strings.TrimSpace(fragment) == "" {
		return nil
	}
	nodes, err := xhtml.ParseFragment(strings.NewReader(fragment), &xhtml.Node{Type: xhtml.ElementNode, Data: "div", DataAtom: atom.Div})
	if err != nil {
		return nil
	}
	base, _ := url.Parse(strings.TrimSpace(baseURL))
	var links []Link
	for _, node := range nodes {
		collectLinks(node, base, &links)
	}
	return links
}

func collectLinks(node *xhtml.Node, base *url.URL, links *[]Link) {
	if node.Type == xhtml.ElementNode && node.Data == "a" {
		var href string
		for _, attr := range node.Attr {
			if strings.EqualFold(attr.Key, "href") {
				href = strings.TrimSpace(attr.Val)
				break
			}
		}
		if target, ok := resolveHTTPURL(href, base); ok {
			label := strings.TrimSpace(whitespace.ReplaceAllString(nodeText(node), " "))
			if label == "" {
				label = target
			}
			*links = append(*links, Link{Text: label, URL: target})
		}
	}
	if node.Type == xhtml.ElementNode {
		switch node.Data {
		case "script", "style", "noscript", "svg":
			return
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		collectLinks(child, base, links)
	}
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
	if (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
		return "", false
	}
	return target.String(), true
}

func nodeText(node *xhtml.Node) string {
	var b strings.Builder
	var walk func(*xhtml.Node)
	walk = func(current *xhtml.Node) {
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

func writeNode(b *strings.Builder, node *xhtml.Node) {
	if node.Type == xhtml.ElementNode {
		switch node.Data {
		case "script", "style", "noscript", "svg":
			return
		case "br", "hr":
			newline(b)
		case "li":
			newline(b)
			b.WriteString("• ")
		}
	}
	if node.Type == xhtml.TextNode {
		text := strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) && r != '\n' {
				return ' '
			}
			return r
		}, node.Data)
		b.WriteString(text)
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		writeNode(b, child)
	}
	if node.Type == xhtml.ElementNode {
		switch node.Data {
		case "p", "div", "article", "section", "header", "footer", "h1", "h2", "h3", "h4", "h5", "h6", "li", "ul", "ol", "blockquote", "pre", "table", "tr":
			newline(b)
			newline(b)
		}
	}
}

func newline(b *strings.Builder) {
	if b.Len() == 0 || strings.HasSuffix(b.String(), "\n") {
		return
	}
	b.WriteByte('\n')
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
