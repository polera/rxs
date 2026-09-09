package render

import (
	"fmt"
	"strings"
	"testing"
)

var benchmarkText string
var benchmarkLinks []Link

func BenchmarkLinkMarkers(b *testing.B) {
	for _, count := range []int{10, 100, 1000} {
		for _, proseBytes := range []int{0, 10000, 100000} {
			b.Run(fmt.Sprintf("links=%d/prose=%d", count, proseBytes), func(b *testing.B) {
				var source strings.Builder
				links := make([]Link, count)
				for i := range links {
					links[i] = Link{Text: "label", URL: "https://example.test/repeated"}
					source.WriteString(strings.Repeat("p", proseBytes/count))
					source.WriteString(linkMarker(i, "start") + " label " + linkMarker(i, "end"))
				}
				text := source.String()
				formatter := func(_ int, _ Link, text string) string { return "[" + text + "]" }
				b.ReportAllocs()
				b.SetBytes(int64(len(text)))
				b.ResetTimer()
				for b.Loop() {
					benchmarkText = formatLinkMarkers(text, links, nil, formatter)
				}
			})
		}
	}
}

func BenchmarkLaTeXScaling(b *testing.B) {
	for _, fixture := range []struct{ name, unit string }{
		{"prose", "Ordinary prose without mathematics. "},
		{"backslashes", `\`},
		{"unmatched", `\(x `},
	} {
		for _, size := range []int{1000, 10000, 100000} {
			b.Run(fmt.Sprintf("%s/bytes=%d", fixture.name, size), func(b *testing.B) {
				text := strings.Repeat(fixture.unit, size/len(fixture.unit))
				b.ReportAllocs()
				b.SetBytes(int64(len(text)))
				b.ResetTimer()
				for b.Loop() {
					benchmarkText = LaTeX(text)
				}
			})
		}
	}
}

func BenchmarkTextWithLinks(b *testing.B) {
	for _, count := range []int{10, 100, 1000} {
		for _, proseBytes := range []int{0, 10000, 100000} {
			b.Run(fmt.Sprintf("links=%d/prose=%d", count, proseBytes), func(b *testing.B) {
				text := strings.Repeat("<p>"+strings.Repeat("p", proseBytes/count)+` <a href="/repeated">label</a></p>`, count)
				b.ReportAllocs()
				b.SetBytes(int64(len(text)))
				b.ResetTimer()
				for b.Loop() {
					benchmarkText, benchmarkLinks = TextWithLinks(text, "https://example.test", nil)
				}
			})
		}
	}
}

func BenchmarkTextWithLinkedMath(b *testing.B) {
	for _, count := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("links=%d", count), func(b *testing.B) {
			text := strings.Repeat(`<p>\(<a href="/a">\al</a><a href="/b">pha</a>+\label{<a href="/c">foo</a>}x+\sqrt[<a href="/d">2</a>]{<a href="/e">y</a>}\)</p>`, count/5)
			b.ReportAllocs()
			b.SetBytes(int64(len(text)))
			b.ResetTimer()
			for b.Loop() {
				benchmarkText, benchmarkLinks = TextWithLinks(text, "https://example.test", nil)
			}
		})
	}
}
