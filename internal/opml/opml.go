// Package opml imports and exports subscription lists.
package opml

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/polera/rxs/internal/domain"
)

const maxImport = 20 << 20

type document struct {
	XMLName xml.Name `xml:"opml"`
	Version string   `xml:"version,attr,omitempty"`
	Head    head     `xml:"head"`
	Body    body     `xml:"body"`
}

type head struct {
	Title string `xml:"title,omitempty"`
}
type body struct {
	Outlines []outline `xml:"outline"`
}
type outline struct {
	Text     string    `xml:"text,attr,omitempty"`
	Title    string    `xml:"title,attr,omitempty"`
	Type     string    `xml:"type,attr,omitempty"`
	XMLURL   string    `xml:"xmlUrl,attr,omitempty"`
	HTMLURL  string    `xml:"htmlUrl,attr,omitempty"`
	Children []outline `xml:"outline"`
}

type Subscription struct {
	Title   string
	FeedURL string
	SiteURL string
}

// Import reads one complete UTF-8 OPML document of at most 20 MiB, including an
// optional leading BOM and its suffix. XML declarations must specify version 1.0.
// DOCTYPE support is limited to opml with an optional SYSTEM or PUBLIC external
// identifier, without an internal subset. DTDs are never fetched or applied.
// This uses encoding/xml, not a full XML/DTD conformance validator.
// The caller owns reader; success requires reaching its actual EOF.
func Import(reader io.Reader) (subscriptions []Subscription, err error) {
	bounded := &io.LimitedReader{R: reader, N: maxImport + 1}
	defer func() {
		if bounded.N == 0 {
			subscriptions, err = nil, errors.New("parse OPML: document is too large")
		}
	}()
	// A shared ByteReader keeps the decoder from buffering past the root.
	buffered := bufio.NewReader(bounded)
	prefix, err := buffered.Peek(3)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("parse OPML: %w", err)
	}
	if bytes.Equal(prefix, []byte("\xef\xbb\xbf")) {
		_, _ = buffered.Discard(3)
	}
	decoder := xml.NewTokenDecoder(&importTokens{Decoder: xml.NewDecoder(buffered)})
	var doc document
	rootSeen, doctypeSeen, declarationAllowed := false, false, true
	for {
		next, err := buffered.ReadByte()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse OPML: %w", err)
		}
		allowDeclaration := declarationAllowed
		declarationAllowed = false
		// Consume only literal XML whitespace. Token normalizes CRLF and also
		// exposes illegal suffix CDATA/character references as CharData.
		if next == ' ' || next == '\t' || next == '\r' || next == '\n' {
			continue
		}
		if err := buffered.UnreadByte(); err != nil {
			return nil, fmt.Errorf("parse OPML: %w", err)
		}
		token, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("parse OPML: %w", err)
		}
		switch token := token.(type) {
		case xml.StartElement:
			if !rootSeen {
				if err := decoder.DecodeElement(&doc, &token); err != nil {
					return nil, fmt.Errorf("parse OPML: %w", err)
				}
				rootSeen = true
				continue
			}
		case xml.Comment:
			continue
		case xml.ProcInst:
			if !strings.EqualFold(token.Target, "xml") || token.Target == "xml" && allowDeclaration {
				continue
			}
		case xml.Directive:
			if !rootSeen && !doctypeSeen {
				doctypeSeen = true
				continue
			}
		}
		if !rootSeen {
			return nil, errors.New("parse OPML: unexpected content before root element")
		}
		return nil, errors.New("parse OPML: unexpected content after root element")
	}
	if !rootSeen {
		return nil, fmt.Errorf("parse OPML: %w", io.EOF)
	}
	var walk func([]outline)
	walk = func(outlines []outline) {
		for _, item := range outlines {
			if feedURL := strings.TrimSpace(item.XMLURL); feedURL != "" {
				title := strings.TrimSpace(item.Title)
				if title == "" {
					title = strings.TrimSpace(item.Text)
				}
				subscriptions = append(subscriptions, Subscription{Title: title, FeedURL: feedURL, SiteURL: strings.TrimSpace(item.HTMLURL)})
			}
			walk(item.Children)
		}
	}
	walk(doc.Body.Outlines)
	return subscriptions, nil
}

func Export(writer io.Writer, feeds []domain.Feed) error {
	doc := document{Version: "2.0", Head: head{Title: "rxs subscriptions"}}
	doc.Body.Outlines = make([]outline, 0, len(feeds))
	for _, source := range feeds {
		doc.Body.Outlines = append(doc.Body.Outlines, outline{
			Text: source.Title, Title: source.Title, Type: "rss",
			XMLURL: source.URL, HTMLURL: source.SiteURL,
		})
	}
	if _, err := io.WriteString(writer, xml.Header); err != nil {
		return err
	}
	encoder := xml.NewEncoder(writer)
	encoder.Indent("", "  ")
	if err := encoder.Encode(doc); err != nil {
		return fmt.Errorf("write OPML: %w", err)
	}
	return nil
}
