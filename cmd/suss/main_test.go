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
