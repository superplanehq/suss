package suss

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/superplanehq/suss/plan"
)

func TestFindProjectRootsDiscoversManifestsAndSkipsDependencyTrees(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), "{}\n")
	writeFile(t, filepath.Join(root, "frontend", "package.json"), "{}\n")
	writeFile(t, filepath.Join(root, "backend", "go.mod"), "module example.com/backend\n\ngo 1.26\n")
	writeFile(t, filepath.Join(root, "apps", "web", "mix.exs"), "defmodule Web.MixProject do\nend\n")
	writeFile(t, filepath.Join(root, "node_modules", "left-pad", "package.json"), "{}\n")
	writeFile(t, filepath.Join(root, "vendor", "module", "go.mod"), "module example.com/vendored\n\ngo 1.26\n")
	writeFile(t, filepath.Join(root, "deps", "plug", "mix.exs"), "defmodule Plug.MixProject do\nend\n")
	writeFile(t, filepath.Join(root, ".hidden", "go.mod"), "module example.com/hidden\n\ngo 1.26\n")

	got, err := findProjectRoots(root)
	if err != nil {
		t.Fatalf("findProjectRoots() error = %v", err)
	}
	want := []string{".", "apps/web", "backend", "frontend"}
	if !slices.Equal(got, want) {
		t.Fatalf("findProjectRoots() = %v, want %v", got, want)
	}
}

func TestFindProjectRootsCollapsesMultipleManifestsInOneDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/mixed\n\ngo 1.26\n")
	writeFile(t, filepath.Join(root, "package.json"), "{}\n")

	got, err := findProjectRoots(root)
	if err != nil {
		t.Fatalf("findProjectRoots() error = %v", err)
	}
	want := []string{"."}
	if !slices.Equal(got, want) {
		t.Fatalf("findProjectRoots() = %v, want %v", got, want)
	}
}

func TestFindProjectRootsSkipsSymlinkedDirectories(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "real")
	writeFile(t, filepath.Join(target, "go.mod"), "module example.com/real\n\ngo 1.26\n")
	if err := os.Symlink(target, filepath.Join(root, "linked")); err != nil {
		t.Fatalf("os.Symlink() error = %v", err)
	}

	got, err := findProjectRoots(root)
	if err != nil {
		t.Fatalf("findProjectRoots() error = %v", err)
	}
	want := []string{"real"}
	if !slices.Equal(got, want) {
		t.Fatalf("findProjectRoots() = %v, want %v", got, want)
	}
}

func TestDetectFillsANodeProjectAndLeavesUncoveredRootsEmpty(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "frontend", "package.json"), `{"scripts": {"test": "vitest"}}`)
	writeFile(t, filepath.Join(root, "frontend", "package-lock.json"), "{}\n")
	writeFile(t, filepath.Join(root, "backend", "go.mod"), "module example.com/backend\n\ngo 1.26\n")

	document, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if len(document.Projects) != 2 {
		t.Fatalf("len(projects) = %d, want 2", len(document.Projects))
	}

	backend := document.Projects[0]
	if backend.Path != "backend" || len(backend.Languages) != 0 || len(backend.Commands) != 0 {
		t.Fatalf("backend = %+v, want an uncovered Go project root", backend)
	}

	frontend := document.Projects[1]
	if frontend.Path != "frontend" {
		t.Fatalf("frontend path = %q", frontend.Path)
	}
	if len(frontend.Languages) == 0 || len(frontend.Commands) == 0 || len(frontend.Preparation) == 0 {
		t.Fatalf("frontend = %+v, want Node findings", frontend)
	}
}

func TestDetectMarksFixtureLikeProjectRoots(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{"name": "app"}`)
	writeFile(t, filepath.Join(root, "testdata", "sample", "package.json"), `{"name": "sample"}`)
	writeFile(t, filepath.Join(root, "examples", "demo", "package.json"), `{"name": "demo"}`)

	document, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}

	facts := map[string]plan.ProjectFact{}
	for _, project := range document.Projects {
		for _, fact := range project.Facts {
			if fact.Name == "project.role" {
				facts[project.Path] = fact
			}
		}
	}
	if _, ok := facts["."]; ok {
		t.Fatalf("root project was marked as a fixture: %+v", facts["."])
	}
	if facts["testdata/sample"].Value != "fixture" || facts["testdata/sample"].Confidence != plan.ConfidenceHigh {
		t.Fatalf("testdata fact = %+v, want high-confidence fixture", facts["testdata/sample"])
	}
	if facts["examples/demo"].Value != "fixture" || facts["examples/demo"].Confidence != plan.ConfidenceMedium {
		t.Fatalf("examples fact = %+v, want medium-confidence fixture", facts["examples/demo"])
	}
}

func TestDetectRejectsAFilePath(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "go.mod")
	writeFile(t, file, "module example.com/file\n\ngo 1.26\n")

	_, err := Detect(file)
	if err == nil {
		t.Fatal("Detect() error = nil, want a not-a-directory error")
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
