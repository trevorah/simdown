# Where the cycles go

This is the Markdown version of section 5.3 of the simdjson paper ("Running
Time Distribution"). Before designing a two-stage CommonMark parser, we
measure where existing CommonMark parsers spend their time, and how much a
simdjson-style stage 1 would cost.

**Summary**

* goldmark, the reference Go CommonMark parser, costs **11–35 cycles per input
  byte**. The C parsers cost less: cmark 8–19 and md4c 5–8.
* A NEON stage 1 that marks newlines and every CommonMark inline special
  character costs **0.37 cycles/byte**. That is 5–8% of md4c's total and
  1–3% of goldmark's.
* goldmark's inline loop spends **about 11 cycles per inline byte just looking
  for the next special character**. That is 16–30% of its total time. Only
  1.5–2.4% of inline bytes are triggers, one every 40–65 bytes, or about one
  per line. This is the part a stage-1 bitmap replaces outright.
* Block parsing costs goldmark **200–575 cycles per line** (32–53% of its
  time). The largest item is trying every block-start parser on every line.
  Where raw HTML is present, HTML-block start detection with `regexp` alone
  costs up to 6 cycles/byte.
* Allocation is the other large, spread-out cost. goldmark allocates **4–10
  bytes per input byte** and 25–72 objects per KB. `mallocgc` accounts for
  14–22% of samples, and garbage collection removes another 9–18% of
  throughput. Together that is roughly 25–35% of goldmark's time. cmark
  spends 9–17% of its time just freeing its tree.

## Setup

| | |
|---|---|
| Machine | Apple M4 (4 performance + 6 efficiency cores, 16 MB L2 per cluster), 32 GB, macOS 27.0 |
| Clock | about 4.4 GHz. Cycle counts are wall time × 4.4 GHz. A dependent add/xor chain measured 4.0–4.4 GHz across runs, so treat cycle figures as ±10%. |
| Go | 1.27.1, goldmark v1.8.6, default CommonMark configuration, `Parser().Parse` only (no rendering) |
| C | Apple clang 21, `-O3`. cmark at `6566d91` with `CMARK_OPT_DEFAULT`, parse plus free. md4c at `7fc1815` with `MD_DIALECT_COMMONMARK` and no-op callbacks. |
| Method | Each figure is the best of five rounds (≥ 2 s total), which filters out background load. Go runs use `GOMAXPROCS=1`, so all GC work is counted against the parser. |

Reproduce with:

```sh
../corpus.sh /tmp/mdcorpus                    # fetch pinned sources and lay out corpus
c/build.sh /tmp/mdcorpus/cmark /tmp/mdcorpus/md4c
GOMAXPROCS=1 go run . -corpus /tmp/mdcorpus/corpus -secs 10
GOMAXPROCS=1 GOGC=off GOMEMLIMIT=6GiB go run . -corpus /tmp/mdcorpus/corpus -secs 10 -cpuprofile /tmp/gm
for c in book k8sdocs k8sref spec; do c/cbench /tmp/mdcorpus/corpus/$c 5; done
```

## Corpus

Four categories, 17.6 MB in total. The generated Kubernetes API reference is
kept separate from the prose docs because it is mostly raw HTML tables and
would otherwise dominate the averages.

| category | source | docs | MB | bytes/line | leaf bytes by kind (goldmark) | trigger density¹ | nodes/KB |
|---|---|---:|---:|---:|---|---:|---:|
| book | *The Rust Programming Language* `src/` | 112 | 1.22 | 47.0 | paragraph 78%, fenced code 8%, HTML 7% | 2.44% | 42.8 |
| k8sdocs | kubernetes/website docs, excluding `reference/` | 523 | 5.03 | 40.9 | paragraph 64%, fenced code 14%, text (tight lists) 13% | 1.53% | 36.7 |
| k8sref | kubernetes/website `docs/reference/` | 1196 | 11.17 | 47.7 | HTML 79%, paragraph 12% | 1.74% | 10.8 |
| spec | CommonMark `spec.txt` | 1 | 0.21 | 21.0 | paragraph 48%, fenced code 23% | 2.30% | 31.3 |

¹ The share of inline-parsed bytes that are one of `` ` * _ [ ] ! < ``, the
characters goldmark dispatches inline parsers on. Adding `\` and `&` barely
changes it (+0.01–0.03 pp). Non-ASCII bytes make up 0–1.1% of every category.

Real-world Markdown is sparse. Prose carries about one special character per
line, and 15–82% of bytes sit in raw leaves (code, HTML) that never reach
inline parsing. That matches the case the simdjson approach was built for:
skip quickly over bytes that don't matter.

## Whole-parser throughput

MB/s, higher is better. Cycles per byte in brackets, lower is better.

| category | goldmark (GC on) | goldmark (`GOGC=off`) | cmark | md4c | NEON stage 1 |
|---|---:|---:|---:|---:|---:|
| book | 116 (37.9) | 128 (34.5) | 270 (16.3) | 655 (6.7) | 11 880 (0.37) |
| k8sdocs | 130 (33.8) | 150 (29.3) | 240 (18.3) | 546 (8.1) | 11 890 (0.37) |
| k8sref | 337 (13.1) | 389 (11.3) | 557 (7.9) | 943 (4.7) | 11 890 (0.37) |
| spec | 148 (29.8) | 180 (24.5) | 237 (18.6) | 653 (6.7) | 11 860 (0.37) |

Raw HTML is cheap for every parser, because an HTML block is just a run of
lines copied through. Prose is the expensive case.

### cmark's phase split

cmark parses blocks in `cmark_parser_feed` and inlines in
`cmark_parser_finish`, so its two phases can be timed directly:

| category | blocks | inlines + finalise | free tree |
|---|---:|---:|---:|
| book | 40% | 43% | 17% |
| k8sdocs | 43% | 43% | 14% |
| k8sref | 66% | 25% | 9% |
| spec | 59% | 29% | 12% |

## goldmark CPU profile

These shares come from `GOGC=off` runs. With GC on, Go's CPU profiler on macOS
misattributes samples: over half landed in `runtime.kevent` and
`runtime.madvise` on runtime threads that were idle. Yet turning GC off made
the parser only 9–18% faster, which shows the samples, not the runtime, were
wrong. With GC off, 90–98% of samples fall inside `Parse`, and each category
has about 1 000 samples (±1.5 pp).

The two phases below don't overlap, and together they account for all of
`Parse`. The indented sub-rows are cumulative and can overlap each other; for
example, inline parsers call the block reader.

| % of samples | book | k8sdocs | k8sref | spec |
|---|---:|---:|---:|---:|
| **Block phase** (`parseBlocks`) | **35.5** | **32.2** | **53.0** | **39.8** |
| &nbsp;&nbsp;try block-start parsers (`openBlocks`) | 26.6 | 16.6 | 27.9 | 13.4 |
| &nbsp;&nbsp;&nbsp;&nbsp;HTML-block start (`htmlBlockParser.Open`) | 17.1 | 0.9 | 17.9 | 0.0 |
| &nbsp;&nbsp;&nbsp;&nbsp;…of which `regexp` | 13.3 | 0.5 | 10.4 | 0.0 |
| &nbsp;&nbsp;HTML-block continue | 0.5 | 0.0 | 5.5 | 0.0 |
| &nbsp;&nbsp;fenced-code continue | 0.6 | 1.6 | 0.5 | 7.3 |
| &nbsp;&nbsp;`closeBlocks` | 3.3 | 5.0 | 5.3 | 6.2 |
| &nbsp;&nbsp;line reader (`AdvanceLine` + `PeekLine`) | 4.0 | 5.3 | 9.3 | 8.3 |
| **Inline phase** (`walkBlock` → `parseBlock`) | **61.9** | **59.1** | **36.0** | **49.9** |
| &nbsp;&nbsp;scan loop itself (`parseBlock` self time) | 28.8 | 29.8 | 16.2 | 22.4 |
| &nbsp;&nbsp;inline parsers (code span, link, raw HTML, emphasis, delimiters) | 16.7 | 9.4 | 5.3 | 9.5 |
| &nbsp;&nbsp;text nodes (`NewTextSegment`, `MergeOrAppendTextSegment`, `AppendChild`) | 15.5 | 13.7 | 9.2 | 16.5 |
| &nbsp;&nbsp;block reader (`Advance` + `PeekLine`) | 5.0 | 7.4 | 6.7 | 5.9 |
| **Cross-cutting** (already inside the phases above) | | | | |
| &nbsp;&nbsp;`mallocgc` | 14.9 | 14.9 | 14.1 | 21.6 |
| &nbsp;&nbsp;&nbsp;&nbsp;`memclrNoHeapPointers` (zeroing spans; mostly under `mallocgc`) | 5.8 | 4.3 | 4.6 | 5.6 |
| &nbsp;&nbsp;&nbsp;&nbsp;`madvise` (the heap reclaiming pages from the OS; under `mallocgc`) | 0.0 | 5.6 | 9.2 | 8.5 |

### What the profile means in cycles

At 4.4 GHz, using the `GOGC=off` totals:

| | book | k8sdocs | k8sref | spec |
|---|---:|---:|---:|---:|
| total, cycles/byte | 34.5 | 29.3 | 11.3 | 24.5 |
| block phase, cycles/**line** | 575 | 385 | 286 | 205 |
| inline scan loop, cycles/**inline byte** | 12.2 | 10.8 | 10.9 | 10.9 |
| HTML-block start detection, cycles/byte | 5.9 | 0.3 | 2.0 | 0.0 |

The scan-loop figure is steady at about 11 cycles per inline byte in every
category. goldmark's loop (`parser.go`, `parseBlock`) tests `IsSpace` and
`IsPunct` on every byte, looks up the inline-parser table, and tracks
backslash escapes. It pays that on every byte, even though fewer than 1 in 40
bytes is a trigger.

## Stage-1 kernels

Each kernel builds, or for the baselines just counts, the set of positions of
`\n` `` ` `` `*` `_` `[` `]` `!` `<` `\` `&` over the concatenated 17.6 MB
corpus. Kernels are in `kernels.go` and `c/cbench.c`.

| kernel | MB/s | cycles/byte |
|---|---:|---:|
| `bytes.Count` newlines only (Go's NEON assembly), bandwidth ceiling | 64 500 | 0.07 |
| **NEON, 10 × `vceqq` + pairwise-add pack to a 64-bit mask** (C) | **11 880** | **0.37** |
| Go scalar, table lookup into a register-held 64-bit mask | 3 090 | 1.4 |
| Go SWAR, 8 bytes per step, exact zero-byte test per character | 2 250 | 2.0 |
| Go per-byte table lookup with a branch (goldmark/cmark loop shape) | 2 000–3 900 | 1.1–2.2 |

In pure Go, SWAR loses to a plain table lookup. Ten equality tests of about
five operations each cost more than one load per byte. A real stage 1 on arm64
needs NEON through Go assembly. A nibble-table (`vqtbl1q_u8`) classifier
like simdjson's will probably beat the 10-compare version above, but that is
untested.
Even the scalar Go bitmap, at 1.4 cycles/byte, is about 8× cheaper than
goldmark's per-inline-byte loop.

## Implications for simdown

1. **Stage 1 is essentially free, and it removes 16–30% of goldmark's
   work.** A NEON bitmap at 0.37 cycles/byte replaces a loop that
   costs about 11 cycles per inline byte. After that, stage 2 visits about one
   trigger per line instead of every byte.
2. **The target is md4c, not simdjson.** md4c already runs at 5–8
   cycles/byte with no tree allocation. Stage 1 is 5–8% of that budget, so a
   simdjson-style parser wins only if stage 2 does less per line and per
   trigger than md4c does. That rules out allocating an AST node per
   inline.
3. **Emit a tape, not a pointer tree.** goldmark allocates 4–10 bytes and
   25–72 objects per KB of input. Allocation (14–22% of samples) plus GC
   (9–18% of throughput) costs it roughly 25–35%, and cmark spends 9–17%
   just freeing its tree. simdjson
   writes a flat tape for the same reason. A `[]uint64` tape of (kind, start,
   end, link) entries needs one growing allocation per document, and an
   `ast.Node` view can be built lazily for callers who want one.
4. **Stage 1 should also produce a line table.** The block phase costs
   200–575 cycles per line. Much of that is running each block-start parser
   against each line, plus line-reader bookkeeping. Newline positions plus
   each line's first non-space byte and indent width, all derivable from the
   stage-1 bitmaps, would allow a single-switch dispatch per line.
5. **Never use `regexp` on the hot path.** HTML-block start detection alone
   costs up to 6 cycles/byte on HTML-heavy input. CommonMark's seven
   HTML-block start conditions are fixed tag lists and prefixes, so a
   hand-written matcher keyed on the byte after `<` covers them.
6. **Go needs assembly for stage 1 on arm64.** Pure Go stays above 1.4
   cycles/byte. Keep the kernel behind a "64 bytes in, bitmasks out" interface,
   with the scalar bitmap as the portable fallback and the reference
   implementation for tests.
