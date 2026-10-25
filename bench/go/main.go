// Command go is the benchmark runner for the Go parsers, simdown and
// goldmark. See ../README.md for the protocol every runner follows.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/renderer/html"

	"simdown"
)

func main() {
	if len(os.Args) < 3 {
		fail("usage: go <parser> render | <parser> bench <bundle> <secs>")
	}
	var toHTML func([]byte) []byte
	switch os.Args[1] {
	case "simdown":
		toHTML = simdown.ToHTML
	case "goldmark":
		// The default parser is CommonMark without extensions; XHTML gives
		// the spec's <br />, and unsafe passes raw HTML through.
		md := goldmark.New(goldmark.WithRendererOptions(html.WithXHTML(), html.WithUnsafe()))
		var buf bytes.Buffer
		toHTML = func(src []byte) []byte {
			buf.Reset()
			if err := md.Convert(src, &buf); err != nil {
				panic(err)
			}
			return buf.Bytes()
		}
	default:
		fail("unknown parser " + os.Args[1])
	}
	switch os.Args[2] {
	case "render":
		render(toHTML)
	case "bench":
		if len(os.Args) != 5 {
			fail("usage: go <parser> bench <bundle> <secs>")
		}
		bench(toHTML, os.Args[3], os.Args[4])
	default:
		fail("unknown mode " + os.Args[2])
	}
}

func render(toHTML func([]byte) []byte) {
	var n [4]byte
	for {
		if _, err := io.ReadFull(os.Stdin, n[:]); err != nil {
			return
		}
		src := make([]byte, binary.LittleEndian.Uint32(n[:]))
		if _, err := io.ReadFull(os.Stdin, src); err != nil {
			fail(err.Error())
		}
		out := toHTML(src)
		binary.LittleEndian.PutUint32(n[:], uint32(len(out)))
		os.Stdout.Write(n[:])
		os.Stdout.Write(out)
	}
}

func bench(toHTML func([]byte) []byte, bundle, secsArg string) {
	b, err := os.ReadFile(bundle)
	if err != nil {
		fail(err.Error())
	}
	var docs [][]byte
	for len(b) >= 4 {
		n := binary.LittleEndian.Uint32(b)
		docs = append(docs, b[4:4+n])
		b = b[4+n:]
	}
	secs, err := strconv.ParseFloat(secsArg, 64)
	if err != nil {
		fail(err.Error())
	}
	round := time.Duration(secs / 6 * float64(time.Second))
	sink := 0
	best := time.Duration(1<<63 - 1)
	// The first round is warm-up and is not counted.
	for r := range 6 {
		passes := 0
		start := time.Now()
		var el time.Duration
		for el < round {
			for _, d := range docs {
				sink += len(toHTML(d))
			}
			passes++
			el = time.Since(start)
		}
		if r > 0 {
			best = min(best, el/time.Duration(passes))
		}
	}
	if sink == 0 {
		fail("no output")
	}
	fmt.Printf("%.9f\n", best.Seconds())
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(2)
}
