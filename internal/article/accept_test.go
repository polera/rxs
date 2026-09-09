package article

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/polera/rxs/internal/domain"
)

func TestUsefulLength(t *testing.T) {
	for _, text := range []string{
		"", " \t\n\r\v\f", "one", "  one \t two\n ",
		"\u00a0\u2003one\u0085two\u2028three\u3000",
		"\u03b1\u03b2 \u4e2d\u6587", "e\u0301 \U0001f469\u200d\U0001f4bb", "a\u200bb",
		"\xff \xfe", strings.Repeat("x", shortFeedLength-1),
		strings.Repeat("x", shortFeedLength), strings.Repeat("x", possiblyPartialSize),
	} {
		t.Run(fmt.Sprintf("%q", text), func(t *testing.T) {
			want := len([]rune(strings.Join(strings.Fields(text), " ")))
			if got := usefulLength(text); got != want {
				t.Fatalf("usefulLength = %d, want %d", got, want)
			}
		})
	}
}

func BenchmarkUsefulLength(b *testing.B) {
	for _, size := range []int{500, 5000, 50000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			text := strings.Repeat("article\twords\u00a0 ", size/15)
			b.ReportAllocs()
			b.SetBytes(int64(len(text)))
			b.ResetTimer()
			for range b.N {
				if usefulLength(text) == 0 {
					b.Fatal("empty normalized length")
				}
			}
		})
	}
}

func TestCandidateClassifiesFeedContentConservatively(t *testing.T) {
	tests := []struct {
		name string
		url  string
		text string
		want bool
	}{
		{name: "empty", url: "https://example.com/empty", want: true},
		{name: "short", url: "https://example.com/short", text: strings.Repeat("brief ", 40), want: true},
		{name: "truncated", url: "https://example.com/truncated", text: strings.Repeat("article words ", 70) + "Continue reading…", want: true},
		{name: "complete medium", url: "https://example.com/complete", text: strings.Repeat("article words ", 70), want: false},
		{name: "long", url: "https://example.com/long", text: strings.Repeat("article words ", 150) + " Read more", want: false},
		{name: "invalid URL", url: "file:///tmp/article", text: "", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Candidate(domain.Entry{URL: test.url, Text: test.text}); got != test.want {
				t.Fatalf("Candidate() = %v, want %v (length %d)", got, test.want, usefulLength(test.text))
			}
		})
	}
}

func TestValidateRequiresLongerMatchingContent(t *testing.T) {
	feedText := strings.Repeat("launch details explain the new spacecraft systems ", 8)
	entry := domain.Entry{Title: "New Spacecraft Launch Details", Text: feedText}
	longBody := feedText + strings.Repeat("additional engineering analysis and mission context ", 20)
	tests := []struct {
		name    string
		content Content
		wantErr bool
	}{
		{name: "matching title", content: Content{Title: "New Spacecraft Launch Details - Example", Text: longBody}},
		{name: "matching opening", content: Content{Title: "Mission report", Text: longBody}},
		{name: "too short", content: Content{Title: entry.Title, Text: "short"}, wantErr: true},
		{name: "not longer", content: Content{Title: entry.Title, Text: feedText + strings.Repeat("x", 200)}, wantErr: true},
		{name: "unrelated page", content: Content{Title: "Account Login Portal", Text: strings.Repeat("Sign in to manage your preferences and browse the home page. ", 20)}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := Validate(entry, test.content)
			if (err != nil) != test.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestValidateUsesLooserRatioForExplicitTruncationCue(t *testing.T) {
	feedText := strings.Repeat("carefully reported details ", 38) + "Continue reading"
	entry := domain.Entry{Title: "Carefully Reported Details", Text: feedText}
	extracted := feedText + strings.Repeat(" additional", 31)
	if usefulLength(extracted)-usefulLength(feedText) < minimumGrowth {
		t.Fatal("test fixture does not meet minimum growth")
	}
	if err := Validate(entry, Content{Title: entry.Title, Text: extracted}); err != nil {
		t.Fatalf("truncated content was rejected: %v", err)
	}
}

func TestInputHashChangesWithEachFeedInput(t *testing.T) {
	updated := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	base := InputHash("https://example.com/one", "<p>body</p>", updated)
	for _, changed := range []string{
		InputHash("https://example.com/two", "<p>body</p>", updated),
		InputHash("https://example.com/one", "<p>changed</p>", updated),
		InputHash("https://example.com/one", "<p>body</p>", updated.Add(time.Second)),
	} {
		if changed == base {
			t.Fatal("changed input produced the same hash")
		}
	}
}
