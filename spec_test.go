package simdown

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// testdata/spec.json holds every example from CommonMark 0.31.2, downloaded
// from https://spec.commonmark.org/0.31.2/spec.json.
type specExample struct {
	Markdown  string `json:"markdown"`
	HTML      string `json:"html"`
	Example   int    `json:"example"`
	StartLine int    `json:"start_line"`
	Section   string `json:"section"`
}

func loadSpec(t testing.TB) []specExample {
	b, err := os.ReadFile("testdata/spec.json")
	if err != nil {
		t.Fatal(err)
	}
	var examples []specExample
	if err := json.Unmarshal(b, &examples); err != nil {
		t.Fatal(err)
	}
	return examples
}

// TestSpec runs each spec example as TestSpec/<section>/<example number>, so
// a single section can be run with e.g. go test -run 'TestSpec/Block_quotes'
// (go test replaces spaces in subtest names with underscores).
func TestSpec(t *testing.T) {
	examples := loadSpec(t)
	passed := 0
	for _, e := range examples {
		t.Run(fmt.Sprintf("%s/%d", e.Section, e.Example), func(t *testing.T) {
			got := string(ToHTML([]byte(e.Markdown)))
			if got != e.HTML {
				t.Errorf("example %d (spec.txt line %d)\nmarkdown: %q\nwant:     %q\ngot:      %q",
					e.Example, e.StartLine, e.Markdown, e.HTML, got)
				return
			}
			passed++
		})
	}
	t.Logf("passed %d of %d examples", passed, len(examples))
}
