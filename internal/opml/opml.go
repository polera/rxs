// Package opml imports and exports subscription lists.
package opml

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"

	"github.com/polera/rxs/internal/domain"
)

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

func Import(reader io.Reader) ([]Subscription, error) {
	decoder := xml.NewDecoder(io.LimitReader(reader, 20<<20))
	var doc document
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("parse OPML: %w", err)
	}
	var subscriptions []Subscription
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
