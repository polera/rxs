package opml

import (
	"bytes"
	"strings"
	"testing"

	"github.com/polera/rxs/internal/domain"
)

func TestImportNestedAndExport(t *testing.T) {
	input := `<opml version="2.0"><body><outline text="Folder"><outline text="Example" xmlUrl="https://example.com/feed" htmlUrl="https://example.com"/></outline></body></opml>`
	subscriptions, err := Import(strings.NewReader(input))
	if err != nil || len(subscriptions) != 1 || subscriptions[0].Title != "Example" {
		t.Fatalf("Import() = %#v, %v", subscriptions, err)
	}
	var output bytes.Buffer
	err = Export(&output, []domain.Feed{{Title: subscriptions[0].Title, URL: subscriptions[0].FeedURL, SiteURL: subscriptions[0].SiteURL}})
	if err != nil || !strings.Contains(output.String(), `xmlUrl="https://example.com/feed"`) {
		t.Fatalf("Export() = %q, %v", output.String(), err)
	}
}
