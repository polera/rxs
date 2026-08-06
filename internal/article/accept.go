package article

import (
	"errors"
	"net/url"
	"strings"
	"unicode"

	"github.com/polera/rxs/internal/domain"
)

const (
	shortFeedLength     = 500
	possiblyPartialSize = 1500
	minimumArticleSize  = 500
	minimumGrowth       = 300
)

// Candidate reports whether an entry conservatively looks partial enough to
// justify an additional page request.
func Candidate(entry domain.Entry) bool {
	target, err := url.Parse(strings.TrimSpace(entry.URL))
	if err != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") {
		return false
	}
	length := usefulLength(entry.Text)
	return length == 0 || length < shortFeedLength ||
		(length < possiblyPartialSize && hasTruncationCue(entry.Text))
}

// Validate checks that extracted content is both materially better than the
// feed copy and recognizably belongs to the same item.
func Validate(entry domain.Entry, content Content) error {
	extractedLength := usefulLength(content.Text)
	if extractedLength < minimumArticleSize {
		return errors.New("extracted article is too short")
	}
	feedLength := usefulLength(entry.Text)
	if feedLength > 0 {
		if extractedLength-feedLength < minimumGrowth {
			return errors.New("extracted article is not materially longer than feed content")
		}
		if hasTruncationCue(entry.Text) {
			if extractedLength*5 < feedLength*6 {
				return errors.New("extracted article is not materially longer than truncated feed content")
			}
		} else if extractedLength*2 < feedLength*3 {
			return errors.New("extracted article is not materially longer than feed content")
		}
	}
	if !titleOverlap(entry.Title, content.Title) && !openingOverlap(entry.Text, content.Text) {
		return errors.New("extracted article does not match the feed item")
	}
	return nil
}

func usefulLength(text string) int {
	return len([]rune(strings.Join(strings.Fields(text), " ")))
}

func hasTruncationCue(text string) bool {
	ending := strings.ToLower(strings.TrimSpace(text))
	ending = strings.TrimRight(ending, "\"'”’)]} ")
	if strings.HasSuffix(ending, "…") || strings.HasSuffix(ending, "...") {
		return true
	}
	ending = strings.TrimRightFunc(ending, func(r rune) bool {
		return unicode.IsPunct(r) || unicode.IsSpace(r)
	})
	for _, cue := range []string{"read more", "continue reading", "keep reading", "view full article", "full article"} {
		if strings.HasSuffix(ending, cue) {
			return true
		}
	}
	return false
}

func titleOverlap(feedTitle, extractedTitle string) bool {
	feed := tokenSet(feedTitle, 0)
	extracted := tokenSet(extractedTitle, 0)
	if len(feed) == 0 || len(extracted) == 0 {
		return false
	}
	overlap := 0
	for token := range feed {
		if _, ok := extracted[token]; ok {
			overlap++
		}
	}
	if len(feed) == 1 {
		return overlap == 1
	}
	return overlap*3 >= len(feed)*2
}

func openingOverlap(feedText, extractedText string) bool {
	feed := tokenSet(feedText, 24)
	if len(feed) < 3 {
		return false
	}
	extracted := tokenSet(extractedText, 100)
	overlap := 0
	for token := range feed {
		if _, ok := extracted[token]; ok {
			overlap++
		}
	}
	return overlap*5 >= len(feed)*3
}

func tokenSet(text string, limit int) map[string]struct{} {
	stop := map[string]struct{}{
		"a": {}, "an": {}, "and": {}, "as": {}, "at": {}, "by": {}, "for": {}, "from": {},
		"in": {}, "is": {}, "it": {}, "of": {}, "on": {}, "or": {}, "the": {}, "to": {}, "with": {},
	}
	tokens := make(map[string]struct{})
	var current []rune
	accepted := 0
	flush := func() bool {
		if len(current) == 0 {
			return false
		}
		token := strings.ToLower(string(current))
		current = current[:0]
		if len([]rune(token)) < 2 {
			return false
		}
		if _, ignored := stop[token]; ignored {
			return false
		}
		tokens[token] = struct{}{}
		accepted++
		return limit > 0 && accepted >= limit
	}
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			current = append(current, unicode.ToLower(r))
			continue
		}
		if flush() {
			return tokens
		}
	}
	flush()
	return tokens
}
