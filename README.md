# simdown

The fastest markdown parser (CommonMark flavour). Uses Go's (experimental) simd support to avoid cpu branching.

## Benchmarks

Markdown to HTML throughput in MB/s (higher is better) on an Apple M4. Every parser runs as plain CommonMark.

| parser | language | [book](https://github.com/rust-lang/book/tree/main/src) | [k8sdocs](https://github.com/kubernetes/website/tree/main/content/en/docs) | [k8sref](https://github.com/kubernetes/website/tree/main/content/en/docs/reference) | [spec](https://github.com/commonmark/commonmark-spec/blob/master/spec.txt) | avg (geomean) |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| **simdown** | Go | **844** | **742** | **1721** | **785** | **959 MB/s** |
| [ox-content](https://github.com/ubugeeei-prod/ox-content) | Rust | 697 | 534 | 1644 | 633 | 789 MB/s |
| [pulldown-cmark](https://github.com/pulldown-cmark/pulldown-cmark) | Rust | 403 | 400 | 1093 | 452 | 531 MB/s |
| [md4c](https://github.com/mity/md4c) | C | 437 | 377 | 728 | 390 | 465 MB/s |
| simdown (no simd) | Go | 262 | 254 | 422 | 288 | 300 MB/s |
| [Markdig](https://github.com/xoofx/markdig) | C# | 265 | 224 | 567 | 173 | 276 MB/s |
| [comrak](https://github.com/kivikakk/comrak) | Rust | 216 | 189 | 535 | 192 | 255 MB/s |
| [cmark](https://github.com/commonmark/cmark) | C | 206 | 185 | 478 | 179 | 239 MB/s |
| [goldmark](https://github.com/yuin/goldmark) | Go | 88 | 101 | 261 | 109 | 126 MB/s |
| [marked](https://github.com/markedjs/marked) | JavaScript | 51 | 45 | 147 | 54 | 65 MB/s |
| [markdown-it](https://github.com/markdown-it/markdown-it) | JavaScript | 49 | 42 | 117 | 51 | 59 MB/s |
| [micromark](https://github.com/micromark/micromark) | JavaScript | 7 | 6 | 10 | 4 | 6 MB/s |

Each column is a set of real docs (the k8sdocs column excludes the API reference in k8sref), fetched at pinned commits.

To run it yourself, from `bench/`:

```sh
./corpus.sh ~/.cache/simdown/mdcorpus       # fetch the docs
./build.sh ~/.cache/simdown/parsers         # build every parser (needs Go, cc, cargo, dotnet, node)
go run . -bin ~/.cache/simdown/parsers -corpus ~/.cache/simdown/mdcorpus/corpus
```

See [bench/README.md](bench/README.md) for how each parser is configured and timed.

## Usage

Requires Go 1.27 or later. To see the benefits you'll need an arm64 machine (e.g. Apple Silicon) and to build with `GOEXPERIMENT=simd`, as below.

```go
import "simdown"

html := simdown.ToHTML([]byte("# Hello\n\nSome *markdown*.\n"))
// <h1>Hello</h1>
// <p>Some <em>markdown</em>.</p>
```

Build with `GOEXPERIMENT=simd` to get the NEON code (arm64 only, as Go 1.27 still keeps `simd/archsimd` behind the experiment):

```sh
GOEXPERIMENT=simd go build
```

Without it you get the boring scalar version that gives identical output, but is about a third as fast.

Note: **Raw HTML is passed through untouched** just as the CommonMark spec says. This means that link destinations such as `javascript:` aren't filtered. If the markdown comes from users, sanitise it!

## How it works

Instead of branching on every input character, we use simd instructions to flag special characters 64 bytes at a time and build an index. This is the two-stage design from [Parsing Gigabytes of JSON per Second](https://arxiv.org/abs/1902.08318) (Langdale & Lemire), applied to markdown.

With an index, the second stage can then parse the markdown and branch on the index entries rather than every char. This is where we get performance gains.

### Example Index

Here is an example of the branchless operations that are used to construct the index(es).

_lines marked ✨ use simd instructions_

```
# Hello, **world**!⏎⏎ Say `hi` to ![Go](go.png) and \*me\* \\o/⏎: input
____________________________________________________1___1__11___: B  (backslash) ✨
___________________11__________________________________________1: LF ✨
_1______1____________1___1____1__1_____________1___1______1_____: W  (space or tab) ✨
1_1_1_1_1_1_1_1_1_1_1_1_1_1_1_1_1_1_1_1_1_1_1_1_1_1_1_1_1_1_1_1_: E  (constant)

// Escapes: the bytes that follow an odd-length run of backslashes.
_____________________________________________________1___1__11__: F   = B << 1
___________________________________________________________1____: OS  = B &~E &~F
____________________________________________________1___1____1__: S   = OS + B
// adding OS clears each odd-started run (carrying out past its end) and
// leaves even-started ones alone, so E ^ S << 1 marks every second byte
// counting from each run's start: odd offsets after an even start, even
// offsets after an odd start. & F keeps the bytes that follow a backslash.
_____________________________________________________1___1__1___: ESC = (E ^ S << 1) & F

// Line index: where each line ends, and its first byte that is not blank
// (the terminator itself, on a blank line).
___________________11__________________________________________1: TERM = LF
1___________________11__________________________________________: ST   = LF << 1 | 1
// if a line starts with blanks, adding 1 at its start ripples a carry
// through them, landing on the line's first byte that is not blank
_1______1_____________1__1____1__1_____________1___1______1_____: C    = (ST & W) + W
1___________________1_1_________________________________________: LEAD = (ST | C) &~W

// Inline index: where an inline construct may begin. Escaped bytes are
// taken out first, so neither `*` in \*me\* opens emphasis, and in \\o/
// only the second backslash is escaped. An escaped punctuation character
// is indexed at its backslash instead, the backslash escape being an
// inline of its own.
_____________________________________________________1___1__1___: XS = ESC & PUNCT  (PUNCT: punctuation, LF or CR) ✨
____________________________________________________1___1__1____: X  = XS >> 1
// each run of ` * _ is one entry, at its first byte
__________________________1__1__________________________________: K  = T &~(T << 1)  (T: unescaped `) ✨
_________11_____11______________________________________________: R  (unescaped *) ✨
_________1______1_______________________________________________: A  = R &~(R << 1)
// "![" is one entry, at the '!'; the '!' after "world" is not part of one
__________________1_______________1_____________________________: N  (unescaped !) ✨
___________________________________1____________________________: L  (unescaped [) ✨
__________________________________1_____________________________: G  = N & (L >> 1)
// the rest: a '[' that is not part of "![", and ] < &
______________________________________1_________________________: O  (] < &) ✨
______________________________________1_________________________: Q  = (L &~(G << 1) | O) &~ESC

_________1______1_________1__1____1___1_____________1___1__1____: INLINE = X | K | A | G | Q
```

## Why Go?

Honestly, to try the new built-in simd package (`simd/archsimd`, still experimental in Go 1.27). Let me know if you recreate this in another language and I'll link to it!

## AI disclosure

AI has been used to help write the code, mainly for the boring spec correctness bits.

## License

MIT, see [LICENSE](LICENSE). The files in `testdata/` are third-party: `spec.json` is derived from the [CommonMark spec](https://spec.commonmark.org/) (CC BY-SA 4.0) and `CaseFolding.txt` is from the [Unicode Character Database](https://www.unicode.org/license.txt) (Unicode License v3).
