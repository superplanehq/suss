package suss

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
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
