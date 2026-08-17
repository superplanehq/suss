package suss

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/superplanehq/suss/schema"
)

var updateGoldens = flag.Bool("update", false, "update golden plan snapshots")

// corpusEntry is one snapshot case. Milestone 1 uses local fixtures.
// Later milestones add gitURL and commit so remote repositories are pinned
// by SHA and checked out into testdata/cache before detection.
type corpusEntry struct {
	name    string
	fixture string
	gitURL  string
	commit  string
	golden  string
}

var corpus = []corpusEntry{
	{name: "empty", fixture: "empty", golden: "empty.json"},
	{name: "go-root", fixture: "go-root", golden: "go-root.json"},
	{name: "polyglot", fixture: "polyglot", golden: "polyglot.json"},
	{name: "nested-ignored", fixture: "nested-ignored", golden: "nested-ignored.json"},
}

func TestCorpusSnapshots(t *testing.T) {
	for _, entry := range corpus {
		t.Run(entry.name, func(t *testing.T) {
			root := entry.repository(t)
			document, err := Detect(root)
			if err != nil {
				t.Fatalf("Detect() error = %v", err)
			}

			got, err := document.MarshalCanonical()
			if err != nil {
				t.Fatalf("MarshalCanonical() error = %v", err)
			}
			if err := schema.Validate(got); err != nil {
				t.Fatalf("schema.Validate() error = %v", err)
			}

			goldenPath := filepath.Join("testdata", "golden", entry.golden)
			if *updateGoldens {
				if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
					t.Fatalf("os.MkdirAll() error = %v", err)
				}
				if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
					t.Fatalf("os.WriteFile() error = %v", err)
				}
			}

			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("os.ReadFile() error = %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("detection output mismatch for %s\n got:\n%s\nwant:\n%s", entry.name, got, want)
			}
		})
	}
}

func (e corpusEntry) repository(t *testing.T) string {
	t.Helper()
	if e.fixture != "" {
		return filepath.Join("testdata", "fixtures", e.fixture)
	}
	if e.gitURL != "" && e.commit != "" {
		t.Fatalf("pinned git corpus entries are not implemented yet: %s@%s", e.gitURL, e.commit)
	}
	t.Fatal("corpus entry has neither a local fixture nor a pinned git commit")
	return ""
}
