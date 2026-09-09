# Store P2: Dates, Refresh, And Enrichment Selection

Measured 2026-09-08 with Go 1.27.0, linux/amd64, modernc SQLite, Intel i5-1240P.
All fixtures use temporary disk databases, the application's single connection,
WAL, and existing pragmas. No external SQLite executable was used.

## Storage And Upgrade Contract

- Migration 004 normalizes application timestamps to fixed-width UTC with nine
  fractional digits, retaining empty dates. Go parses legacy RFC3339Nano offsets
  and SQLite's old `CURRENT_TIMESTAMP` feed creation dates without truncating
  nanoseconds. Invalid nonempty dates fail and roll back instead of being erased.
- Per-feed and global expression indexes match publication/update fallback and
  descending ID ties. The old publication-only index is dropped. Administrative
  `schema_migrations.applied_at` timestamps are not used for application ordering.
- Migration 005 adds current input hash, eligibility, an eligibility-only partial
  index including the hash, and a persisted candidate-policy version.
- `Open` backfills before exposing the store. Bodies are processed in 256-row
  batches; metadata and policy version commit together. Schema DDL can already be
  committed if backfill fails, but version 0 remains and the next open retries.
  This is a startup-wide transaction, not constant-time migration or a hard byte
  memory limit. Memory depends on the largest batch's bodies.
- Bump `enrichmentPolicyVersion` whenever `article.Candidate` or `article.InputHash`
  semantics change. Startup then recomputes all derived metadata, including
  historical entries absent from subsequent 200 responses or only seeing 304s.
  Attempts/overlays are not deleted. Eligibility changes alone do not repeat an
  attempt for an unchanged hash. Current-policy opens only check the version.
- Hashes are computed through the unchanged article API from semantic UTC time,
  not from the new persisted date string. Normalization does not invalidate old
  successful or failed attempts.
- Candidate selection filters eligibility and attempted/current hash mismatch in
  SQL before returning bodies, with SQL `LIMIT` after filtering. It pins the
  partial index because modernc's planner otherwise sometimes selects the
  unfiltered per-feed date index without `ANALYZE` statistics.
- Completed downloads still use the P1 transactional source-hash, entry identity,
  subscription identity, and already-attempted guards. Derived selection metadata
  is not trusted to authorize a save. Legitimate failed replacements keep overlays.
- The feed/service API is unchanged. Shipped migrations 001-003 are unchanged.

## Measurement Method

The baseline was captured after adding benchmarks and before production edits.
Baseline refresh/history/list samples used `-benchtime=1x -count=1`; contended
`SetRead` used `3x`. Final history and contention samples used `3x -count=1`.
Final refresh/list numbers below are medians of three `3x` samples. These are
short, nonisolated local measurements, not benchstat significance claims. Other
agents were working on the host; some package checks ran during measurements.

Refresh fixtures contain 100/1,000/10,000 short-summary entries. Initial refresh
starts with no entries; deletion is outside the timer. Unchanged refresh repeats
the same 200-response payload, not a 304. History fixtures contain
1,000/10,000/100,000 entries with roughly 2.4 KiB HTML/325-byte text for partial
entries and 4.1 KiB HTML/2.1 KiB text for full entries. Older-pending fixtures have
ten pending entries behind all newer attempted entries. Fixture creation and
metadata backfill are outside timed loops.

`BenchmarkSetReadDuringEnrichment` starts timing after background selection owns
the sole connection, then writes while selection repeats. Its allocations include
the background goroutine; they are not standalone `SetRead` allocations. It reports
average operation latency, not percentiles or a production responsiveness bound.

## Results

Refresh time in milliseconds:

| Entries | Baseline Initial | Final Initial | Baseline Unchanged | Final Unchanged |
| --- | ---: | ---: | ---: | ---: |
| 100 | 24.49 | 7.48 | 5.45 | 6.44 |
| 1,000 | 91.72 | 67.60 | 48.65 | 54.04 |
| 10,000 | 678.66 | 760.94 | 644.26 | 678.47 |

Prepared UPSERT/`RETURNING` removes the separate ID lookup, but this combined change
also adds eligibility classification, hashing, wider dates, and index writes on
every refresh. It is **not** an across-the-board refresh speedup. At 10,000 entries,
unchanged refresh went from 16,080,384 B/op and 459,521 allocs/op to approximately
40,725,245 B/op and 559,556 allocs/op. Initial refresh allocations are similar.

Enrichment selection time in milliseconds:

| History | Case | Baseline | Final | Baseline B/op | Final B/op | Baseline Allocs/op | Final Allocs/op |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 1,000 | None eligible | 58.20 | 0.273 | 16,328,584 | 3,904 | 22,786 | 37 |
| 10,000 | None eligible | 688.65 | 0.327 | 163,291,472 | 3,904 | 229,793 | 37 |
| 100,000 | None eligible | 7,168.40 | 0.165 | 1,632,815,816 | 3,904 | 2,299,805 | 37 |
| 1,000 | All attempted | 33.81 | 1.363 | 8,150,200 | 3,904 | 33,794 | 37 |
| 10,000 | All attempted | 353.51 | 10.371 | 81,494,024 | 3,909 | 339,890 | 37 |
| 100,000 | All attempted | 3,172.00 | 90.639 | 814,930,248 | 3,904 | 3,400,965 | 37 |
| 1,000 | Older pending | 35.86 | 1.283 | 8,149,000 | 40,560 | 33,768 | 237 |
| 10,000 | Older pending | 309.45 | 10.875 | 81,492,544 | 40,560 | 339,879 | 237 |
| 100,000 | Older pending | 3,530.36 | 89.370 | 814,920,120 | 40,560 | 3,400,878 | 237 |

Selection still scans eligible metadata and joins attempt records. It is not
constant-time when all eligible historical entries have been attempted, and it
still occupies the single connection for that scan.

Contended `SetRead` time in milliseconds, all-attempted history:

| History | Baseline | Final |
| --- | ---: | ---: |
| 1,000 | 35.66 | 1.86 |
| 10,000 | 321.50 | 20.33 |
| 100,000 | 3,254.16 | 99.57 |

Listing the newest 1,000 full-body entries, time in milliseconds:

| History | Baseline Global | Final Global | Baseline Per-Feed | Final Per-Feed |
| --- | ---: | ---: | ---: | ---: |
| 1,000 | 74.26 | 24.67 | 51.87 | 26.37 |
| 10,000 | 324.91 | 25.16 | 259.61 | 21.51 |
| 100,000 | 2,566.34 | 22.46 | 2,637.48 | 24.59 |

Returned body allocations remain approximately 8.5 MB and 19,053 allocations per
1,000-entry result. `TestOrderingQueryPlans` checks actual modernc plans with and
without `ANALYZE`: global/per-feed/unread listings and enrichment use their intended
indexes without a temporary ORDER BY B-tree.

## Index And Write Costs

At 10,000 ineligible entries, the old date index occupied 233,472 bytes. Final
per-feed/global date indexes occupy 839,680/823,296 bytes, and the empty partial
candidate index occupies 4,096 bytes. Allocated database bytes rose from 82,419,712
to 83,861,504. With all 10,000 entries eligible, the candidate index occupies
2,252,800 bytes. These are allocated pages (including unused page capacity),
measured by `PRAGMA freelist_count` changes across transactional index drops and
rollback; they are not serialized key sizes. WAL bytes are excluded.

For 1,000 unchanged eligible entries, median milliseconds across three 20-iteration
write samples were 53.26 with current indexes, 60.89 retaining the obsolete index,
53.52 without the global date index, and 45.83 without the candidate index. The
candidate index has a visible write cost; the global-index difference was below
local measurement noise. Dropping the old index avoids redundant space/write work.
This smaller write fixture uses short searchable text and is not directly comparable
to the general refresh fixture above.

## Reproduction

```sh
go test -count=1 ./internal/store ./internal/feed
go test -race -count=1 ./internal/store ./internal/feed
go vet ./internal/store ./internal/feed
go test ./internal/store -run '^TestOrderingQueryPlans$' -v -count=1
go test ./internal/store -run '^$' -bench 'Benchmark(Refresh|EnrichmentHistory|EntriesHistory|SetReadDuringEnrichment)$' -benchmem -benchtime=3x -count=3
go test ./internal/store -run '^$' -bench BenchmarkRefreshIndexWrites -benchmem -benchtime=20x -count=3
go test ./internal/store -run '^$' -bench BenchmarkIndexFootprint -benchmem -benchtime=1x
```

The footprint benchmark's elapsed time measures the diagnostic DROP/rollback probe,
not application writes. Startup backfill latency, native non-Linux behavior,
multi-process migration contention, and statistically isolated comparisons were
not measured. The existing single-process migration assumption is unchanged.
