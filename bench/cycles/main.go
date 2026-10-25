// Command bench measures where a CommonMark parser spends its time, in the
// spirit of section 5.3 of the simdjson paper. It parses a corpus with
// goldmark (the reference Go CommonMark parser), reports throughput and
// allocation rate, writes a CPU profile for phase attribution, characterises
// the corpus, and times candidate stage-1 kernels as a lower bound on what a
// byte scan costs.
//
// Usage: go run . -corpus DIR [-cpuprofile PREFIX] [-secs N]
//
// DIR holds one subdirectory per corpus category, each full of .md files.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

var (
	corpusDir  = flag.String("corpus", "", "directory of corpus categories")
	cpuprofile = flag.String("cpuprofile", "", "write goldmark CPU profiles to PREFIX.<category>.pprof")
	secs       = flag.Float64("secs", 3, "minimum seconds per measurement")
)

type category struct {
	name  string
	docs  [][]byte
	bytes int
}

func main() {
	flag.Parse()
	if *corpusDir == "" {
		log.Fatal("-corpus is required")
	}
	cats := loadCorpus(*corpusDir)

	ghz := calibrateGHz()
	fmt.Printf("estimated clock: %.2f GHz (dependent add/xor chain)\n\n", ghz)

	fmt.Println("## Corpus")
	for _, c := range cats {
		characterise(c)
	}

	fmt.Println("\n## goldmark parse (no rendering)")
	md := goldmark.New()
	var all category
	for _, c := range cats {
		mbps, allocB, allocs := measureParse(md, c)
		fmt.Printf("%-6s %7.1f MB/s  %5.2f cycles/byte  %5.2f alloc bytes/byte  %6.1f allocs/KB\n",
			c.name, mbps, ghz*1e3/mbps, allocB, allocs)
		all.docs = append(all.docs, c.docs...)
		all.bytes += c.bytes
	}

	if *cpuprofile != "" {
		all.name = "all"
		for _, c := range append(cats, all) {
			profile(md, c, *cpuprofile+"."+c.name+".pprof")
		}
	}

	fmt.Println("\n## Stage-1 kernels over the whole corpus")
	joined := bytes.Join(all.docs, nil)
	for _, k := range kernels {
		mbps := measure(len(joined), func() { k.fn(joined) })
		fmt.Printf("%-34s %8.1f MB/s  %5.3f cycles/byte\n", k.name, mbps, ghz*1e3/mbps)
	}
}

func profile(md goldmark.Markdown, c category, path string) {
	f, err := os.Create(path)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := pprof.StartCPUProfile(f); err != nil {
		log.Fatal(err)
	}
	defer pprof.StopCPUProfile()
	measureParse(md, c)
}

func loadCorpus(dir string) []category {
	subdirs, err := os.ReadDir(dir)
	if err != nil {
		log.Fatal(err)
	}
	var cats []category
	for _, sd := range subdirs {
		if !sd.IsDir() {
			continue
		}
		files, _ := filepath.Glob(filepath.Join(dir, sd.Name(), "*.md"))
		sort.Strings(files)
		c := category{name: sd.Name()}
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				log.Fatal(err)
			}
			c.docs = append(c.docs, b)
			c.bytes += len(b)
		}
		cats = append(cats, c)
	}
	return cats
}

// measure runs fn in rounds for at least -secs in total and returns the best
// round's MB/s for n bytes processed per call. Taking the best round filters
// out interference from other processes and efficiency-core scheduling.
func measure(n int, fn func()) float64 {
	const rounds = 5
	fn() // warm up
	best := 0.0
	for range rounds {
		iters := 0
		start := time.Now()
		for time.Since(start).Seconds() < *secs/rounds {
			fn()
			iters++
		}
		best = max(best, float64(n*iters)/time.Since(start).Seconds()/1e6)
	}
	return best
}

func measureParse(md goldmark.Markdown, c category) (mbps, allocBytesPerByte, allocsPerKB float64) {
	p := md.Parser()
	parseAll := func() {
		for _, d := range c.docs {
			p.Parse(text.NewReader(d))
		}
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	parseAll()
	runtime.ReadMemStats(&after)
	allocBytesPerByte = float64(after.TotalAlloc-before.TotalAlloc) / float64(c.bytes)
	allocsPerKB = float64(after.Mallocs-before.Mallocs) / float64(c.bytes) * 1024
	return measure(c.bytes, parseAll), allocBytesPerByte, allocsPerKB
}

// calibrateGHz estimates the core clock by timing a chain of dependent
// add/xor operations, each of which has one cycle of latency on current arm64
// and x86 cores.
func calibrateGHz() float64 {
	const n = 1 << 28
	best := 0.0
	for range 5 {
		start := time.Now()
		x := addChain(n)
		el := time.Since(start).Seconds()
		if x == 42 {
			fmt.Print("") // keep x live
		}
		best = max(best, 4*n/el/1e9)
	}
	return best
}

//go:noinline
func addChain(n int) uint64 {
	var x uint64
	for i := 0; i < n; i++ {
		// four dependent single-cycle ops; the loop counter runs in parallel
		x += uint64(i)
		x ^= uint64(i)
		x += uint64(i)
		x ^= uint64(i)
	}
	return x
}

// characterise prints the shape of a corpus category as goldmark sees it:
// how its bytes split between raw leaves (code, HTML) and inline-parsed
// leaves, and how dense CommonMark's special characters are in the latter.
func characterise(c category) {
	var lines, nonASCII, rawBytes, inlineBytes, triggers, specials, nodes, inlineNodes int
	kinds := map[string]int{}
	p := goldmark.New().Parser()
	for _, d := range c.docs {
		lines += bytes.Count(d, []byte{'\n'})
		for _, b := range d {
			if b >= 0x80 {
				nonASCII++
			}
		}
		root := p.Parse(text.NewReader(d))
		ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
			if !entering {
				return ast.WalkContinue, nil
			}
			nodes++
			if n.Type() == ast.TypeInline {
				inlineNodes++
			}
			if n.Type() != ast.TypeBlock || n.Lines().Len() == 0 {
				return ast.WalkContinue, nil
			}
			segs := n.Lines()
			for i := 0; i < segs.Len(); i++ {
				seg := segs.At(i)
				s := seg.Value(d)
				kinds[n.Kind().String()] += len(s)
				if n.IsRaw() {
					rawBytes += len(s)
					continue
				}
				inlineBytes += len(s)
				for _, b := range s {
					switch b {
					case '`', '*', '_', '[', ']', '!', '<':
						triggers++
						specials++
					case '\\', '&':
						specials++
					}
				}
			}
			return ast.WalkContinue, nil
		})
	}
	pct := func(n int) float64 { return 100 * float64(n) / float64(c.bytes) }
	fmt.Printf("%-6s %4d docs %6.2f MB  %5.1f bytes/line  %4.1f%% non-ASCII  raw leaves %4.1f%%  inline leaves %4.1f%%  "+
		"triggers %.2f%% of inline bytes (+\\& %.2f%%)  %.1f nodes/KB (%.0f%% inline)\n",
		c.name, len(c.docs), float64(c.bytes)/1e6, float64(c.bytes)/float64(lines), pct(nonASCII),
		pct(rawBytes), pct(inlineBytes),
		100*float64(triggers)/float64(inlineBytes), 100*float64(specials)/float64(inlineBytes),
		float64(nodes)/float64(c.bytes)*1024, 100*float64(inlineNodes)/float64(nodes))
	var ks []string
	for k := range kinds {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool { return kinds[ks[i]] > kinds[ks[j]] })
	fmt.Print("       leaf bytes by kind:")
	for _, k := range ks {
		fmt.Printf(" %s %.1f%%", k, pct(kinds[k]))
	}
	fmt.Println()
}
