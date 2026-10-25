package simdown

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"simdown/internal/index"
)

// The corpus tests run over a directory of Markdown files, such as the one
// bench/corpus.sh lays out:
//
//	SIMDOWN_CORPUS=/tmp/mdcorpus/corpus go test -bench Corpus
//
// With SIMDOWN_CMARK naming a cmark binary, TestCorpusMatchesCmark also
// compares every document's HTML with cmark --unsafe.

func loadCorpus(tb testing.TB) map[string][][]byte {
	dir := os.Getenv("SIMDOWN_CORPUS")
	if dir == "" {
		tb.Skip("SIMDOWN_CORPUS not set")
	}
	cats := map[string][][]byte{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		tb.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		filepath.WalkDir(filepath.Join(dir, e.Name()), func(p string, de fs.DirEntry, err error) error {
			if err == nil && !de.IsDir() && filepath.Ext(p) == ".md" {
				b, err := os.ReadFile(p)
				if err != nil {
					tb.Fatal(err)
				}
				cats[e.Name()] = append(cats[e.Name()], b)
			}
			return nil
		})
	}
	return cats
}

func TestCorpusMatchesCmark(t *testing.T) {
	cmark := os.Getenv("SIMDOWN_CMARK")
	if cmark == "" {
		t.Skip("SIMDOWN_CMARK not set")
	}
	for name, docs := range loadCorpus(t) {
		bad := 0
		for _, doc := range docs {
			cmd := exec.Command(cmark, "--unsafe")
			cmd.Stdin = bytes.NewReader(doc)
			want, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			got := ToHTML(doc)
			if !bytes.Equal(got, want) {
				if bad < 3 {
					i := 0
					for i < len(got) && i < len(want) && got[i] == want[i] {
						i++
					}
					lo := max(0, i-120)
					t.Errorf("%s: first difference at output byte %d\nwant %q\ngot  %q",
						name, i, want[lo:min(len(want), i+120)], got[lo:min(len(got), i+120)])
				}
				bad++
			}
		}
		t.Logf("%s: %d of %d documents differ from cmark", name, bad, len(docs))
	}
}

func BenchmarkCorpus(b *testing.B) {
	for name, docs := range loadCorpus(b) {
		benchToHTML(b, name, docs)
	}
}

// BenchmarkSpec times ToHTML over all the spec examples, which are short and
// markup-dense: they render to 1.85 times their input, where the corpus
// renders to 1.03-1.14 times, so they show the cost of sizing the output.
func BenchmarkSpec(b *testing.B) {
	var docs [][]byte
	for _, ex := range loadSpec(b) {
		docs = append(docs, []byte(ex.Markdown))
	}
	benchToHTML(b, "all", docs)
}

// benchToHTML times ToHTML over docs, and reports out/in, the ratio of
// output to input bytes.
func benchToHTML(b *testing.B, name string, docs [][]byte) {
	b.Run(name, func(b *testing.B) {
		in, out := 0, 0
		for _, d := range docs {
			in += len(d)
			out += len(ToHTML(d))
		}
		b.SetBytes(int64(in))
		b.ReportAllocs()
		for b.Loop() {
			for _, d := range docs {
				ToHTML(d)
			}
		}
		b.ReportMetric(float64(out)/float64(in), "out/in")
	})
}

// BenchmarkCorpusStage1 times the structural index alone.
func BenchmarkCorpusStage1(b *testing.B) {
	for name, docs := range loadCorpus(b) {
		n := 0
		for _, d := range docs {
			n += len(d)
		}
		b.Run(name, func(b *testing.B) {
			b.SetBytes(int64(n))
			var ix index.Index
			for b.Loop() {
				for _, d := range docs {
					ix.Build(d)
				}
			}
		})
	}
}

// BenchmarkCorpusParse times stages 1-3 without rendering, which is what
// bench/cycles/README.md measures for goldmark, cmark and md4c.
func BenchmarkCorpusParse(b *testing.B) {
	for name, docs := range loadCorpus(b) {
		n := 0
		for _, d := range docs {
			n += len(d)
		}
		b.Run(name, func(b *testing.B) {
			b.SetBytes(int64(n))
			var p parser
			for b.Loop() {
				for _, d := range docs {
					p.parse(d)
				}
			}
		})
	}
}

// BenchmarkCorpusRender times rendering alone: each document
// is parsed once into a parser of its own, and only render is in the loop.
// Throughput is in input bytes, as for the other corpus benchmarks, so that
// Corpus is roughly CorpusParse plus CorpusRender.
func BenchmarkCorpusRender(b *testing.B) {
	for name, docs := range loadCorpus(b) {
		benchRender(b, name, docs)
	}
}

// BenchmarkSpecRender times rendering of the spec examples, one benchmark per
// section, which isolates the renderer's cost per construct. It needs no
// corpus.
func BenchmarkSpecRender(b *testing.B) {
	sections := map[string][][]byte{}
	var order []string
	for _, ex := range loadSpec(b) {
		if sections[ex.Section] == nil {
			order = append(order, ex.Section)
		}
		sections[ex.Section] = append(sections[ex.Section], []byte(ex.Markdown))
	}
	for _, s := range order {
		benchRender(b, s, sections[s])
	}
}

func benchRender(b *testing.B, name string, docs [][]byte) {
	b.Run(name, func(b *testing.B) {
		ps := make([]*parser, len(docs))
		n := 0
		for i, d := range docs {
			ps[i] = parsed(d)
			n += len(d)
			if got, want := ps[i].render(nil), ToHTML(d); !bytes.Equal(got, want) {
				b.Fatalf("render of a parsed document differs from ToHTML on %q", d)
			}
		}
		b.SetBytes(int64(n))
		b.ReportAllocs()
		// One output buffer, reused, so that the loop measures rendering
		// rather than growing a fresh slice per document as ToHTML does.
		var out []byte
		for b.Loop() {
			for _, p := range ps {
				out = p.render(out[:0])
			}
		}
	})
}

// parsed runs stages 1-3 on src into a parser of its own.
func parsed(src []byte) *parser {
	p := new(parser)
	p.parse(src)
	return p
}
