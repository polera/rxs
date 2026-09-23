# Paged article browsing

The article pane now fetches 64 metadata rows at a time, plus one row to detect
whether another page exists. The cursor is the stored effective date and ID,
matching the date indexes in migration 004. Reverse queries support previous
pages and jumping to the oldest result; the reader fetches just the selected
article body. Search retains its existing literal substring semantics and pages
through matching results. No database migration is needed.

## Initial measurements

One-iteration, non-isolated local samples on Go 1.27, Linux/amd64,
Intel i5-1240P, using the existing disk-backed history fixtures:

| Query | 10,000 articles | 100,000 articles | Allocations at 100,000 |
| --- | ---: | ---: | ---: |
| Prior 1,000 full articles, per feed | 30.3 ms | 24.2 ms | 8.5 MB |
| First 65 metadata rows, per feed | 1.14 ms | 0.69 ms | 55 KB |
| First 65 matching substring results | 1.24 ms | 0.84 ms | 63 KB |
| Absent substring, per feed | 32.6 ms | 289 ms | 26 KB |

These are directional single samples, not statistically isolated before/after
claims. The matching fixture has common summary text near the beginning of the
index, while the absent search scans all article text. No-result search is the
remaining scale-sensitive path. Adding a trigram full-text index could improve
it, but would duplicate stored text and add writes during refresh/enrichment;
the present measurements do not justify imposing that cost on all libraries.

To rerun the focused comparisons:

```sh
go test ./internal/store -run '^$' -bench 'BenchmarkEntriesHistory/10000/' -benchmem -benchtime=1x -count=1
go test ./internal/store -run '^$' -bench 'BenchmarkEntriesPageHistory/10000/' -benchmem -benchtime=1x -count=1
```

The benchmark pattern also matches the 100,000-entry subcase. Query-plan tests
verify that the normal and seek pages use the effective-date index.
