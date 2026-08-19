package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/schema"
)

func TestRunJSONEmitsSchemaValidPlan(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/cli\n\ngo 1.26\n")
	writeFile(t, filepath.Join(root, "frontend", "package.json"), "{}\n")

	var stdout, stderr bytes.Buffer
	code := run([]string{"--json", root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run() = %d, stderr = %s", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	encoded := stdout.Bytes()
	if err := schema.Validate(encoded); err != nil {
		t.Fatalf("schema.Validate() error = %v", err)
	}

	var document plan.Document
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if err := document.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if len(document.Projects) != 2 {
		t.Fatalf("len(projects) = %d, want 2", len(document.Projects))
	}
	if document.Projects[0].Path != "." || document.Projects[1].Path != "frontend" {
		t.Fatalf("paths = %q, %q", document.Projects[0].Path, document.Projects[1].Path)
	}
}

func TestRunWithoutJSONRendersThePlan(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "backend", "go.mod"), "module example.com/backend\n\ngo 1.26\n")

	var stdout, stderr bytes.Buffer
	code := run([]string{root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run() = %d, stderr = %s", code, stderr.String())
	}
	got := stdout.String()
	if !strings.Contains(got, "Languages: go") {
		t.Fatalf("stdout = %q, want a covered Go project", got)
	}
	if !strings.Contains(got, "go build ./...") {
		t.Fatalf("stdout = %q, want inferred go build", got)
	}
}

func TestRunWithoutJSONNamesTheRepositoryRoot(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "ecto")
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/ecto\n\ngo 1.26\n")

	var stdout, stderr bytes.Buffer
	code := run([]string{root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run() = %d, stderr = %s", code, stderr.String())
	}
	got := stdout.String()
	if !strings.Contains(got, "ecto\n====") {
		t.Fatalf("stdout = %q, want the repository directory as the root heading", got)
	}
	if strings.Contains(got, "Project: .") {
		t.Fatalf("stdout = %q, want no JSON-path project heading", got)
	}
}

func TestRunHelpExitsZero(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := run([]string{"--help"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run() = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Usage: suss") {
		t.Fatalf("stdout = %q, want usage text", stdout.String())
	}
	got := stdout.String()
	for _, flag := range []string{"--json", "--all-commands", "--all-projects", "--all-environments", "--uninterpreted", "--evidence"} {
		if !strings.Contains(got, flag) {
			t.Fatalf("stdout = %q, want %s", got, flag)
		}
	}
}

func TestParseArgsAcceptsDetailFlags(t *testing.T) {
	t.Parallel()

	opts, err := parseArgs([]string{"--all-commands", "--all-projects", "--all-environments", "--uninterpreted", "--evidence", "repo"})
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if opts.path != "repo" {
		t.Fatalf("path = %q, want repo", opts.path)
	}
	if !opts.allCommands || !opts.allProjects || !opts.allEnvironments || !opts.uninterpreted || !opts.evidence {
		t.Fatalf("flags = %+v, want all command-detail flags set", opts)
	}
}

func TestRunOmitsSupportingDetailByDefault(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{"scripts": {"test": "vitest", "e2e": "playwright"}}`+"\n")
	writeFile(t, filepath.Join(root, "package-lock.json"), "{}\n")

	var stdout, stderr bytes.Buffer
	code := run([]string{root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run() = %d, stderr = %s", code, stderr.String())
	}
	got := stdout.String()
	if strings.Contains(got, "Uninterpreted commands:") || strings.Contains(got, "Evidence:") {
		t.Fatalf("stdout = %q, want supporting detail omitted by default", got)
	}
}

func TestRunIncludesSupportingDetailWhenRequested(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{"scripts": {"test": "vitest", "e2e": "playwright"}}`+"\n")
	writeFile(t, filepath.Join(root, "package-lock.json"), "{}\n")

	var stdout, stderr bytes.Buffer
	code := run([]string{"--uninterpreted", "--evidence", root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run() = %d, stderr = %s", code, stderr.String())
	}
	got := stdout.String()
	if !strings.Contains(got, "Uninterpreted commands:") || !strings.Contains(got, "e2e") {
		t.Fatalf("stdout = %q, want uninterpreted commands", got)
	}
	if !strings.Contains(got, "Evidence:") || !strings.Contains(got, "package.json") {
		t.Fatalf("stdout = %q, want evidence", got)
	}
}

func TestRunIncludesEveryInterpretedCommandWhenRequested(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{"scripts": {"test": "vitest", "test:unit": "vitest --run"}}`+"\n")
	writeFile(t, filepath.Join(root, "package-lock.json"), "{}\n")

	var compactOut, compactErr bytes.Buffer
	if code := run([]string{root}, &compactOut, &compactErr); code != 0 {
		t.Fatalf("compact run() = %d, stderr = %s", code, compactErr.String())
	}
	if !strings.Contains(compactOut.String(), "use --all-commands to inspect") || strings.Contains(compactOut.String(), "npm run test:unit") {
		t.Fatalf("compact stdout = %q, want a human-readable expansion hint", compactOut.String())
	}

	var expandedOut, expandedErr bytes.Buffer
	if code := run([]string{"--all-commands", root}, &expandedOut, &expandedErr); code != 0 {
		t.Fatalf("expanded run() = %d, stderr = %s", code, expandedErr.String())
	}
	for _, command := range []string{"npm test", "npm run test:unit"} {
		if !strings.Contains(expandedOut.String(), command) {
			t.Fatalf("expanded stdout = %q, want %q", expandedOut.String(), command)
		}
	}
}

func TestRunUnknownFlagExitsTwo(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := run([]string{"--wat"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run() = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown flag") {
		t.Fatalf("stderr = %q, want an unknown-flag error", stderr.String())
	}
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("os.MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
}
