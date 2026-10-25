// Command bench benchmarks simdown against other Markdown parsers. Each
// parser is driven through a runner built by build.sh, which this command
// first checks against the spec examples and then times over each corpus
// category. It prints a Markdown table of the results.
//
// Usage: go run . -bin DIR -corpus DIR [-secs N] [-only a,b]
package main

import (
	"bufio"
	"cmp"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

var (
	binDir    = flag.String("bin", "", "directory build.sh built the runners into")
	corpusDir = flag.String("corpus", "", "directory of corpus categories, as corpus.sh lays out")
	specFile  = flag.String("spec", "../testdata/spec.json", "spec examples")
	secs      = flag.Float64("secs", 6, "seconds to time each parser on each category")
	only      = flag.String("only", "", "comma-separated parsers to run (default all)")
	failures  = flag.Bool("failures", false, "log every spec example a parser fails")
)

type parser struct {
	name, lang string
	// cmd is the runner's command line, before the mode arguments.
	cmd func(bin string) []string
}

func runner(path string, args ...string) func(string) []string {
	return func(bin string) []string {
		return append([]string{filepath.Join(bin, path)}, args...)
	}
}

var parsers = []parser{
	{"simdown", "Go", runner("go-runner", "simdown")},
	{"simdown (no simd)", "Go", runner("go-runner-scalar", "simdown")},
	{"goldmark", "Go", runner("go-runner", "goldmark")},
	{"cmark", "C", runner("c-runner", "cmark")},
	{"md4c", "C", runner("c-runner", "md4c")},
	{"pulldown-cmark", "Rust", runner("rust-runner", "pulldown-cmark")},
	{"ox-content", "Rust", runner("rust-runner", "ox-content")},
	{"comrak", "Rust", runner("rust-runner", "comrak")},
	{"markdig", "C#", func(bin string) []string {
		return []string{"dotnet", filepath.Join(bin, "dotnet-runner", "runner.dll"), "markdig"}
	}},
	{"markdown-it", "JavaScript", node("markdown-it")},
	{"marked", "JavaScript", node("marked")},
	{"micromark", "JavaScript", node("micromark")},
}

func node(name string) func(string) []string {
	return func(bin string) []string {
		return []string{"node", filepath.Join(bin, "js", "runner.mjs"), name}
	}
}

type category struct {
	name   string
	bundle string
	bytes  int
}

type result struct {
	parser
	exact, normalized int
	mbps              []float64
}

func main() {
	flag.Parse()
	if *binDir == "" || *corpusDir == "" {
		log.Fatal("-bin and -corpus are required")
	}
	// Go runs with one OS thread, as cycles/README.md does, so its garbage
	// collection is counted against the parser.
	if os.Getenv("GOMAXPROCS") == "" {
		os.Setenv("GOMAXPROCS", "1")
	}
	examples := loadSpec()
	tmp, err := os.MkdirTemp("", "parsers")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	cats := writeBundles(tmp)

	var results []result
	for _, p := range parsers {
		if *only != "" && !slices.Contains(strings.Split(*only, ","), p.name) {
			continue
		}
		cmd := p.cmd(*binDir)
		if _, err := exec.LookPath(cmd[0]); err != nil {
			log.Printf("skipping %s: %v", p.name, err)
			continue
		}
		if cmd[0] == "node" || cmd[0] == "dotnet" {
			if _, err := os.Stat(cmd[1]); err != nil {
				log.Printf("skipping %s: %v", p.name, err)
				continue
			}
		}
		r := result{parser: p}
		r.exact, r.normalized = conformance(cmd, examples)
		log.Printf("%s: %d/%d spec examples (%d byte for byte)", p.name, r.normalized, len(examples), r.exact)
		for _, c := range cats {
			s := timeRunner(cmd, c.bundle)
			r.mbps = append(r.mbps, float64(c.bytes)/s/1e6)
			log.Printf("%s: %s %.1f MB/s", p.name, c.name, r.mbps[len(r.mbps)-1])
		}
		results = append(results, r)
	}
	slices.SortStableFunc(results, func(a, b result) int {
		return cmp.Compare(geomean(b.mbps), geomean(a.mbps))
	})
	printTable(cats, results, len(examples))
}

// The runner protocol, render and bench modes, is described in README.md.

type example struct{ Markdown, HTML string }

func loadSpec() []example {
	b, err := os.ReadFile(*specFile)
	if err != nil {
		log.Fatal(err)
	}
	var ex []example
	if err := json.Unmarshal(b, &ex); err != nil {
		log.Fatal(err)
	}
	return ex
}

// conformance returns how many spec examples the runner renders as the spec
// does, byte for byte and after normalization.
func conformance(cmd []string, examples []example) (exact, normalized int) {
	c := exec.Command(cmd[0], append(cmd[1:], "render")...)
	c.Stderr = os.Stderr
	in, _ := c.StdinPipe()
	outPipe, _ := c.StdoutPipe()
	if err := c.Start(); err != nil {
		log.Fatal(err)
	}
	out := bufio.NewReader(outPipe)
	var n [4]byte
	for i, ex := range examples {
		binary.LittleEndian.PutUint32(n[:], uint32(len(ex.Markdown)))
		in.Write(n[:])
		io.WriteString(in, ex.Markdown)
		if _, err := io.ReadFull(out, n[:]); err != nil {
			log.Fatalf("%s: example %d: %v", cmd, i+1, err)
		}
		html := make([]byte, binary.LittleEndian.Uint32(n[:]))
		if _, err := io.ReadFull(out, html); err != nil {
			log.Fatal(err)
		}
		if string(html) == ex.HTML {
			exact++
		}
		if normalize(string(html)) == normalize(ex.HTML) {
			normalized++
		} else if *failures {
			log.Printf("%s: example %d\n%q\nwant %q\ngot  %q", cmd[len(cmd)-1], i+1, ex.Markdown, ex.HTML, html)
		}
	}
	in.Close()
	if err := c.Wait(); err != nil {
		log.Fatalf("%s: %v", cmd, err)
	}
	return exact, normalized
}

func timeRunner(cmd []string, bundle string) float64 {
	c := exec.Command(cmd[0], append(cmd[1:], "bench", bundle, fmt.Sprint(*secs))...)
	c.Stderr = os.Stderr
	out, err := c.Output()
	if err != nil {
		log.Fatalf("%s: %v", cmd, err)
	}
	s, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		log.Fatalf("%s: %v", cmd, err)
	}
	return s
}

// writeBundles writes each corpus category as one bundle file in dir.
func writeBundles(dir string) []category {
	entries, err := os.ReadDir(*corpusDir)
	if err != nil {
		log.Fatal(err)
	}
	var cats []category
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		c := category{name: e.Name(), bundle: filepath.Join(dir, e.Name())}
		var bundle []byte
		filepath.WalkDir(filepath.Join(*corpusDir, e.Name()), func(p string, de fs.DirEntry, err error) error {
			if err == nil && !de.IsDir() && filepath.Ext(p) == ".md" {
				b, err := os.ReadFile(p)
				if err != nil {
					log.Fatal(err)
				}
				bundle = binary.LittleEndian.AppendUint32(bundle, uint32(len(b)))
				bundle = append(bundle, b...)
				c.bytes += len(b)
			}
			return nil
		})
		if err := os.WriteFile(c.bundle, bundle, 0o644); err != nil {
			log.Fatal(err)
		}
		cats = append(cats, c)
	}
	return cats
}

func printTable(cats []category, results []result, nExamples int) {
	fmt.Printf("| parser | language | spec (of %d) | exact |", nExamples)
	for _, c := range cats {
		fmt.Printf(" %s |", c.name)
	}
	fmt.Print(" geomean |\n| --- | --- | ---: | ---: |")
	for range cats {
		fmt.Print(" ---: |")
	}
	fmt.Println(" ---: |")
	for _, r := range results {
		fmt.Printf("| %s | %s | %d | %d |", r.name, r.lang, r.normalized, r.exact)
		for _, m := range r.mbps {
			fmt.Printf(" %.0f |", m)
		}
		fmt.Printf(" %.0f |\n", geomean(r.mbps))
	}
}

func geomean(xs []float64) float64 {
	s := 0.0
	for _, x := range xs {
		s += math.Log(x)
	}
	return math.Exp(s / float64(len(xs)))
}
