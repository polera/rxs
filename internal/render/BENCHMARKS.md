# Render P2 Measurements

Measured 2026-09-08 with Go 1.27.0, linux/amd64, Intel Core i5-1240P.
Benchmark fixtures were added and run against the original implementation before
production changes. The same fixtures were run after the final nested-anchor fix.
All edits for this work are confined to `internal/render`; exported APIs are unchanged.

```sh
go test ./internal/render -run '^$' -bench 'Benchmark(LinkMarkers|LaTeXScaling|TextWithLinks)$' -benchmem -benchtime=100ms -count=2 -cpu=1
```

Times below are the range of the two samples, not confidence intervals. Allocation
columns use the first sample (small byte-count differences between samples reflect
allocator/pool amortization). Inputs are built outside timed loops. Links use repeated
URLs and labels; prose bytes are independently varied and distributed evenly between
links. `LinkMarkers` includes a bracket-adding formatter. `TextWithLinks` includes HTML
parsing, URL resolution, rendering, and nil-formatter marker substitution.

## Link Marker Substitution

Time is microseconds/op; allocation columns are bytes/op and allocations/op.

| Links | Prose Bytes | Before us/op | After us/op | Before B/op | After B/op | Before Allocs | After Allocs |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 10 | 0 | 7.14-10.11 | 5.52-5.97 | 2880 | 896 | 40 | 31 |
| 10 | 10000 | 27.74-27.99 | 7.81-7.82 | 106085 | 11360 | 40 | 31 |
| 10 | 100000 | 200.43-201.27 | 27.18-27.31 | 1065492 | 106981 | 40 | 31 |
| 100 | 0 | 125.87-145.15 | 52.28-52.78 | 282779 | 10384 | 400 | 301 |
| 100 | 10000 | 325.21-326.93 | 55.52-55.83 | 1326033 | 19856 | 400 | 301 |
| 100 | 100000 | 1981.43-2078.90 | 74.20-74.35 | 10655649 | 112021 | 407 | 301 |
| 1000 | 0 | 6150.89-6285.32 | 551.59-552.46 | 29387958 | 116982 | 5509 | 4489 |
| 1000 | 10000 | 8128.86-8294.49 | 554.68-555.54 | 40288974 | 125174 | 5516 | 4489 |
| 1000 | 100000 | 25421.46-26088.57 | 571.82-618.23 | 131146620 | 215288 | 5585 | 4489 |

## End-To-End HTML With Links

Time is microseconds/op; allocation columns are bytes/op and allocations/op.

| Links | Prose Bytes | Before us/op | After us/op | Before B/op | After B/op | Before Allocs | After Allocs |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 10 | 0 | 63.20-63.86 | 46.55-47.34 | 22001 | 19104 | 306 | 290 |
| 10 | 10000 | 515.30-523.02 | 148.19-148.29 | 263798 | 122494 | 347 | 322 |
| 10 | 100000 | 4515.38-4535.98 | 1003.10-1009.83 | 2567298 | 1093939 | 358 | 323 |
| 100 | 0 | 650.15-651.85 | 417.80-425.73 | 425592 | 146103 | 2753 | 2641 |
| 100 | 10000 | 1400.28-1402.51 | 558.68-564.64 | 1622377 | 257190 | 3060 | 2943 |
| 100 | 100000 | 7260.20-7266.23 | 1478.17-1493.40 | 12314553 | 1256080 | 3078 | 2947 |
| 1000 | 0 | 12126.70-12399.46 | 4545.55-4576.79 | 29848848 | 1456360 | 30073 | 29032 |
| 1000 | 10000 | 15548.28-16071.28 | 4662.02-4791.42 | 41761201 | 1497906 | 32083 | 31033 |
| 1000 | 100000 | 39855.89-40215.57 | 6146.17-6155.48 | 134192189 | 2573989 | 33159 | 32035 |

## LaTeX Scanner Scaling

Time is microseconds/op. Prose repeats `Ordinary prose without mathematics. `;
backslashes repeat a single backslash; unmatched repeats `\(x ` without a closer.
Prose uses whole repeats, so its actual lengths are 972, 9972, and 99972 bytes.
All after samples below allocate **0 B/op, 0 allocations/op**.

| Input | Target Bytes | Before us/op | After us/op | Before B/op | Before Allocs |
| --- | ---: | ---: | ---: | ---: | ---: |
| Prose | 1000 | 32.68-32.69 | 0.481-0.481 | 3320 | 9 |
| Prose | 10000 | 332.30-333.93 | 4.610-4.610 | 46584 | 16 |
| Prose | 100000 | 3356.16-3360.87 | 45.882-45.883 | 514808 | 24 |
| Backslashes | 1000 | 384.02-390.64 | 2.466-2.469 | 3320 | 9 |
| Backslashes | 10000 | 35892.93-35962.94 | 24.112-24.127 | 46586 | 16 |
| Backslashes | 100000 | 3457730.46-3468622.81 | 240.676-240.773 | 514816 | 24 |
| Unmatched | 1000 | 55.55-55.92 | 12.588-12.592 | 3320 | 9 |
| Unmatched | 10000 | 2385.16-2386.31 | 125.183-128.319 | 46584 | 16 |
| Unmatched | 100000 | 207868.60-208261.47 | 1274.53-1292.20 | 514816 | 24 |

## Verification And Limits

```sh
go test ./internal/render -count=1
go test ./internal/render -race -count=1
go vet ./internal/render
go test ./internal/render -run '^$' -fuzz '^FuzzLaTeX$' -fuzztime=30s -parallel=2
go test ./internal/render -run '^$' -fuzz '^FuzzHTML$' -fuzztime=30s -parallel=2
git diff --check -- internal/render
```

Initial P2 checks passed. Those bounded fuzz runs executed 181645 LaTeX and 111292 HTML cases,
including cached coverage seeds. Earlier 20-second runs also passed. LaTeX fuzzing
checks valid-UTF-8 output from the scanner and expression parser, unchanged ordinary
prose, and a simple delimiter oracle for short inputs. HTML fuzzing checks UTF-8,
link-marker removal/index order, and literal inline-code isolation. Inputs are capped
at 32 KiB and 16 KiB respectively; these are fuzz budgets, not public API input limits.

Regressions cover code/pre isolation, literal tagless content, nested quotes/tables,
MathJax single conversion, formulas spanning inline emphasis/span nodes, Unicode
arguments/whitespace, unsupported commands, currency/escapes, empty/image anchors,
repeated URLs, multiline labels, Unicode label whitespace, nil formatters, table
header policy, and malformed nested anchors across tables.

Prose regions end at block boundaries and literal/already-rendered output. Inline
links and `br` remain in the same region: link annotations are kept outside the TeX
grammar and carried through transformations, while `br` contributes a newline.
Display formulas and parenthesized math can span those newlines; single-dollar math
still rejects newlines under the existing currency heuristic. Code retains the existing
inline whitespace normalization; preformatted content retains the existing indentation
and newline policy. Tagless input follows the existing preformatted-input policy and
is consequently literal, not a math region. TeX remains a small best-effort converter;
after 128 recursive parser frames, the unparsed remainder is preserved literally.

Normal link substitution consumes the remaining suffix rather than rebuilding the
document. Malformed nested anchors revisit only formatted ancestor labels to retain
existing formatter order/behavior; their work includes the total size of those labels.
Arbitrary formatter work/output is not bounded. `Links` still uses `TextWithLinks`;
the optional link-only extraction optimization was not undertaken.

These are short local samples, not a benchstat significance study or CI thresholds.
The largest baseline pathological cases ran only once per sample because each
operation exceeded the requested benchmark duration. Other agents were active in the
workspace, so machine load was not controlled. No dependencies or external benchmark
tools were installed. Tests were intentionally scoped to render; other packages and
cross-platform execution were not validated by this work.

## Inline Boundary Review Fix

The tables above record the initial P2 implementation before this review follow-up.
The hyperlink/line-break limitation is now resolved. New regressions assert agreement
between `Text` and nil-formatter `TextWithLinks`, exact formatted label placement,
unchanged link identity/order, and OSC 8 output. Cases include linked variables,
Unicode/script arguments, fractions, split commands, root degrees, display formulas
spanning `br`, nested quotes/tables, literal code/pre, and single conversion.

Render race tests, vet, and diff checks passed after the first follow-up. Its three fuzz runs
used `-fuzztime=20s -parallel=2`: `FuzzMathLinkAnnotations` executed 136132 cases,
`FuzzHTML` 50201, and `FuzzLaTeX` 23180. At that stage the annotation target capped
inputs at 4 KiB and checked one marker pair at arbitrary rune boundaries. It checked
rendered math and marker preservation, but not multiple adjacent pairs or final link
fallback formatting; those gaps are addressed in the second follow-up below.

A one-sample `-benchtime=100ms -cpu=1` rerun of `BenchmarkLaTeXScaling` and
`BenchmarkTextWithLinks` retained zero allocations for all scanner fixtures. At
100000 bytes, prose took 45.95 us/op, backslashes 241.55 us/op, and unmatched openers
1174.46 us/op. End-to-end 1000-link/100000-prose-byte rendering took 5.972 ms/op,
2573982 B/op, and 32035 allocations/op. These are spot checks, not new significance
claims; they do not measure the added cost of annotations in math-heavy documents.

## Adjacent Link Review Fix

Both second-review reports were reproduced before fixing them: a command split across
adjacent anchors leaked raw markers, and an erased `label` argument reappeared through
link fallback text. Token annotations now retain source order rather than regrouping
opening and closing markers. A collapsed command emits its text once, in the first
contributing span; surplus spans remain empty. Command annotations close before any
annotated arguments, keeping originally adjacent links separate.

Fallback eligibility is recorded from the original source label, independently of the
public `Link` value. Erased nonempty labels remain empty, with their original metadata
and formatter callbacks retained. Genuine empty/whitespace/image anchors retain their
existing fallback behavior. No public API changed, and the parent's existing `nosec`
guard was preserved.

Regressions now cover adjacent split commands, removed macros, roots, fractions,
quotes/tables, and fallback distinctions. Partition tests split formulas into 1/2/3/5
rune anchors, asserting final `TextWithLinks(nil)`/`Text` equivalence, callback order,
marker-free callback labels/output, and balanced OSC 8 output, including empty labels.
The annotation fuzz target now uses up to three adjacent nonempty spans and checks the
final formatter path as well as marker source order. It removes source whitespace to
focus on math conversion rather than deliberate empty-anchor fallback or HTML whitespace
policy; those policies have separate regression tests. The 4 KiB input cap remains.

Tests, race tests, vet, and scoped gosec v2.29.0 passed; gosec reported zero issues and
one existing `nosec`. Latest 30-second, two-worker fuzz runs passed: annotation 112880
cases, HTML 38553, and LaTeX 110889. All validation was restricted to render.

A second-follow-up spot check used the same Go/platform and
`-benchtime=100ms -count=1 -cpu=1`. Scanner fixtures still allocate zero bytes. At
100000 bytes, prose took 45.92 us/op, backslashes 242.44 us/op, and unmatched openers
1190.95 us/op. The existing 1000-link/100000-prose-byte end-to-end fixture took
5.902 ms/op, 2577334 B/op, and 32045 allocations/op. The new fallback eligibility
slice accounts for additional allocations; historical tables above are unchanged.

The new `BenchmarkTextWithLinkedMath` fixture contains five links per paragraph,
covering an adjacent split command, an erased label, and a linked root degree/value.
These are after-only samples: there is no correctness-equivalent before measurement
for this new fixture, and no statistical speedup claim.

| Links | us/op | B/op | Allocations/op |
| ---: | ---: | ---: | ---: |
| 10 | 53.91 | 24841 | 378 |
| 100 | 494.27 | 204355 | 3473 |
| 1000 | 5197.61 | 2113411 | 37432 |

## Practical Math Corpus Support

The renderer remains a lightweight text converter, not a TeX layout engine. In
addition to delimited prose and MathJax scripts, it recognizes Eli Bendersky's
`latex-math` and `/images/math/` object/image forms. Object textual fallback is
parsed directly (including undelimited TeX and HTML entities); display wrappers
or `align-center` math objects/images retain block boundaries. A fully display-
delimited image `alt` is also treated as display math. Ordinary media keeps its
existing fallback behavior.

The audited corpus subset includes vectors and hat accents, selected blackboard
and calligraphic Unicode letters with plain-letter fallback, angle brackets,
common dots/operators/arrows, transparent delimiter sizing, readable boxes,
invisible `left`/`right` delimiters, and `cases`/`aligned` line handling. This is
not general TeX: spacing and glyph composition are terminal-oriented, unknown
commands remain visible, and unsupported environments or layout primitives are
not typeset. Accents decorate the first visible base grapheme before its rendered
scripts: `\vec{V_A}` becomes `V⃗_(A)` and `\hat{x^2}` becomes `x̂²`. A wide accent
over multiple bases is approximated on the first base, so `\widehat{AB}` becomes
`ÂB`. Nested recognized environments restore their enclosing `cases` behavior.
