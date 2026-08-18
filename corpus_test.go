package suss

import (
	"bytes"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/superplanehq/suss/schema"
)

var updateGoldens = flag.Bool("update", false, "update golden plan snapshots")

var cacheMu sync.Mutex

// corpusEntry is one snapshot case. Local fixtures live under testdata/fixtures.
// Remote repositories are pinned by commit SHA and shallow-fetched into
// testdata/cache, which is gitignored and reused across runs.
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
	{name: "go-simple", fixture: "go-simple", golden: "go-simple.json"},
	{name: "go-workspace", fixture: "go-workspace", golden: "go-workspace.json"},
	{name: "go-gha", fixture: "go-gha", golden: "go-gha.json"},
	{name: "polyglot", fixture: "polyglot", golden: "polyglot.json"},
	{name: "nested-ignored", fixture: "nested-ignored", golden: "nested-ignored.json"},
	{name: "node-simple", fixture: "node-simple", golden: "node-simple.json"},
	{name: "node-competing-lockfiles", fixture: "node-competing-lockfiles", golden: "node-competing-lockfiles.json"},
	{name: "node-config-only", fixture: "node-config-only", golden: "node-config-only.json"},
	{name: "fixture-paths", fixture: "fixture-paths", golden: "fixture-paths.json"},
	{name: "gha-node", fixture: "gha-node", golden: "gha-node.json"},
	{name: "gha-workspace", fixture: "gha-workspace", golden: "gha-workspace.json"},
	{name: "make-simple", fixture: "make-simple", golden: "make-simple.json"},
	{name: "compose-env", fixture: "compose-env", golden: "compose-env.json"},
	{name: "ruby-rails", fixture: "ruby-rails", golden: "ruby-rails.json"},
	{name: "php-laravel", fixture: "php-laravel", golden: "php-laravel.json"},
	{name: "rust-simple", fixture: "rust-simple", golden: "rust-simple.json"},
	{name: "rust-gha", fixture: "rust-gha", golden: "rust-gha.json"},
	{name: "rust-workspace", fixture: "rust-workspace", golden: "rust-workspace.json"},
	{
		name:   "chalk",
		gitURL: "https://github.com/chalk/chalk.git",
		commit: "661317e6f91fe7c90306c2c48ea9354562ee9146",
		golden: "chalk.json",
	},
	{
		name:   "excalidraw",
		gitURL: "https://github.com/excalidraw/excalidraw.git",
		commit: "e160ff7ba0641fba729c528482de5277ffb19c58",
		golden: "excalidraw.json",
	},
	{
		name:   "mermaid",
		gitURL: "https://github.com/mermaid-js/mermaid.git",
		commit: "9ac963518542234a23a6bd2880d74391aaa06236",
		golden: "mermaid.json",
	},
	{
		name:   "cobra",
		gitURL: "https://github.com/spf13/cobra.git",
		commit: "adbc8813901bba65827259daa8e22ff94ec1f30e",
		golden: "cobra.json",
	},
	{
		name:   "caddy",
		gitURL: "https://github.com/caddyserver/caddy.git",
		commit: "0bbda5c728e7694ba929aa4fa0c549f1387d3a3f",
		golden: "caddy.json",
	},
	{
		name:   "plausible",
		gitURL: "https://github.com/plausible/analytics.git",
		commit: "54d8af8ecfab4943cc29852ce2fc072746799307",
		golden: "plausible.json",
	},
	{
		name:   "listmonk",
		gitURL: "https://github.com/knadh/listmonk.git",
		commit: "670c01717d48647093335cc23a6be6f4b79c3b6b",
		golden: "listmonk.json",
	},
	{
		name:   "superplane",
		gitURL: "https://github.com/superplanehq/superplane.git",
		commit: "cb916a3de1d9bd26e26711e51943863aaa7f058e",
		golden: "superplane.json",
	},
	{
		name:   "operately",
		gitURL: "https://github.com/operately/operately.git",
		commit: "063fdefe80a9ddd1d8db005e146f1040f117e6ba",
		golden: "operately.json",
	},
	{
		name:   "once-campfire",
		gitURL: "https://github.com/basecamp/once-campfire.git",
		commit: "f1aec6133667a7486d521430f871bc420e18ec2b",
		golden: "once-campfire.json",
	},
	{
		name:   "rake",
		gitURL: "https://github.com/ruby/rake.git",
		commit: "ec87311d9339f6ed9ff7143fa8e449a97ba34f1b",
		golden: "rake.json",
	},
	{
		name:   "monolog",
		gitURL: "https://github.com/Seldaek/monolog.git",
		commit: "57eb1028342134e701e77c617565d51b6e5a2a53",
		golden: "monolog.json",
	},
	{
		name:   "koel",
		gitURL: "https://github.com/koel/koel.git",
		commit: "dfec91ff290509c622ff7cf392fb5e506841ee2b",
		golden: "koel.json",
	},
	{
		name:   "clap",
		gitURL: "https://github.com/clap-rs/clap.git",
		commit: "3716f9f4289594b43abec42b2538efd1a90ff897",
		golden: "clap.json",
	},
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
		return checkoutPinned(t, e.gitURL, e.commit)
	}
	t.Fatal("corpus entry has neither a local fixture nor a pinned git commit")
	return ""
}

func checkoutPinned(t *testing.T, gitURL, commit string) string {
	t.Helper()

	cacheMu.Lock()
	defer cacheMu.Unlock()

	dest := filepath.Join("testdata", "cache", cacheKey(gitURL, commit))
	if gitHEAD(dest) == commit {
		return dest
	}

	if err := os.RemoveAll(dest); err != nil {
		t.Fatalf("os.RemoveAll(%s) error = %v", dest, err)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatalf("os.MkdirAll(%s) error = %v", dest, err)
	}

	commands := [][]string{
		{"git", "init", "--quiet"},
		{"git", "remote", "add", "origin", gitURL},
		{"git", "fetch", "--quiet", "--depth=1", "origin", commit},
		{"git", "checkout", "--quiet", "--detach", "FETCH_HEAD"},
	}
	for _, args := range commands {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dest
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	if head := gitHEAD(dest); head != commit {
		t.Fatalf("checked out %s, want %s", head, commit)
	}
	return dest
}

func gitHEAD(dir string) string {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func cacheKey(gitURL, commit string) string {
	trimmed := strings.TrimSuffix(gitURL, ".git")
	trimmed = strings.TrimPrefix(trimmed, "https://")
	trimmed = strings.TrimPrefix(trimmed, "http://")
	trimmed = strings.ReplaceAll(trimmed, ":", "/")
	return filepath.Join(filepath.FromSlash(trimmed), commit)
}
