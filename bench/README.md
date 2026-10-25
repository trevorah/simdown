# simdown against other parsers

This suite times simdown against ten other parsers, a mix of the most widely
used and the fastest, in C, Rust, Go, C# and JavaScript. Every parser does the same job: it turns Markdown into HTML,
because `ToHTML` is simdown's whole API. The suite also checks each parser
against the spec examples. That check confirms each parser runs as a plain
CommonMark parser, with raw HTML passed through and no extensions, so that
every parser does the same work.

From `bench/`:

```sh
./corpus.sh ~/.cache/simdown/mdcorpus          # the corpus cycles/README.md uses
./build.sh ~/.cache/simdown/parsers            # fetch and build every runner
go run . -bin ~/.cache/simdown/parsers -corpus ~/.cache/simdown/mdcorpus/corpus
```

The last command prints a Markdown table: one row per
parser, MB/s per corpus category, and the geometric mean of those figures.
`-secs` sets how long each parser is timed on each category (default 6),
`-only simdown,md4c` limits the run to the named parsers, and `-failures`
logs every spec example a parser gets wrong.

`build.sh` needs Go, a C compiler, `cargo`, `dotnet` and `node`. It skips
any runner whose toolchain is missing, and the driver then skips that parser.
Homebrew installs `rustup` keg-only, so put `$(brew --prefix rustup)/bin` on
`PATH` first.

## Runners

Each language gets one runner program. It takes the parser's name as its
first argument and follows one protocol:

- `<runner> <parser> render` reads documents from stdin and writes each
  document's HTML to stdout. Every message is framed as a 4-byte
  little-endian length followed by that many bytes.
- `<runner> <parser> bench <bundle> <secs>` loads `bundle`, a file of
  documents framed the same way, and renders all of them over and over for
  `secs` seconds, in six rounds. It throws away the first round as warm-up
  and prints the best of the other five as seconds per pass over the bundle.

The driver writes one bundle per corpus category. Documents are decoded into
the language's string type while the bundle loads, so decoding is never
timed. Each parser is called the fastest way its API allows a caller that
converts many documents in a row: output buffers are reused where the API
takes one, and ox-content keeps its arena and renderer the way its own
Node binding does.

| parser | version | runner | configuration |
| --- | --- | --- | --- |
| simdown | this tree | `go/` | `ToHTML`, built with `GOEXPERIMENT=simd` |
| simdown (no simd) | this tree | `go/` | `ToHTML`, built without it, so the scalar twins replace the NEON code |
| goldmark | 1.8.6 | `go/` | defaults, `html.WithXHTML()`, `html.WithUnsafe()` |
| cmark | `6566d91` | `c/` | `cmark_markdown_to_html`, `CMARK_OPT_UNSAFE` |
| md4c | `7fc1815` | `c/` | `md_html`, `MD_DIALECT_COMMONMARK`, `MD_HTML_FLAG_XHTML` |
| pulldown-cmark | 0.13.5 | `rust/` | `Options::empty()`, `push_html` |
| ox-content | 3.4.0 | `rust/` | default `ParserOptions`; renderer with XHTML and no added links or `target` attributes |
| comrak | 0.57.0 | `rust/` | defaults, `render.unsafe` |
| Markdig | 1.5.0 | `dotnet/` | default pipeline |
| markdown-it | 15.0.3 | `js/` | `commonmark` preset |
| marked | 18.1.1 | `js/` | `gfm: false` |
| micromark | 4.0.3 | `js/` | `allowDangerousHtml`, `allowDangerousProtocol` |

Go runs with `GOMAXPROCS=1` (unless it is set already), as in
`cycles/README.md`, so goldmark's garbage collection counts against it. Node and
.NET keep their default background GC and JIT threads. The Rust runner is
built with LTO and one codegen unit, and the C runner with `-O3`.

## Conformance

The table has two conformance columns. `spec` counts the examples a parser
gets right after the HTML normalization that the spec's own test runner
applies (`normalize.go` ports `test/normalize.py`). `exact` counts byte-for-byte
matches, which is the standard simdown holds itself to. Most of the gap
between the two columns is cosmetic. md4c, for example, writes no newline
after `<li>` and writes `'` as `&#x27;`.

ox-content's renderer always gives headings an `id`, and no option turns
that off. Those ids cause 40 of its failures. Most of the other 12 are link
destinations it does not percent-encode. The `id` slugs are work the other
renderers don't do, so its throughput includes them.
