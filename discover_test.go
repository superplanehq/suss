package suss

import (
	"bytes"
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

func TestFindProjectRootsDiscoversMakefileAndEnvExample(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Makefile"), "test:\n\tgo test ./...\n")
	writeFile(t, filepath.Join(root, "deploy", ".env.example"), "DATABASE_URL=\n")

	got, err := findProjectRoots(root)
	if err != nil {
		t.Fatalf("findProjectRoots() error = %v", err)
	}
	want := []string{".", "deploy"}
	if !slices.Equal(got, want) {
		t.Fatalf("findProjectRoots() = %v, want %v", got, want)
	}
}

func TestDetectRunsMakeAndEnvfileWithoutLanguageManifests(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Makefile"), "test: ; go test ./...\n")
	writeFile(t, filepath.Join(root, ".env.example"), "API_TOKEN=\n")

	document, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if len(document.Projects) != 1 {
		t.Fatalf("len(projects) = %d, want 1", len(document.Projects))
	}
	project := document.Projects[0]
	if !hasEnv(project, "API_TOKEN", true, false) {
		t.Fatalf("requirements = %+v, want API_TOKEN from .env.example", project.Requirements)
	}
	var sawMakeTest bool
	for _, command := range project.Commands {
		if command.Name == "test" && derefRun(command.Run) == "make test" {
			sawMakeTest = true
		}
	}
	if !sawMakeTest {
		t.Fatalf("commands = %+v, want make test from a Makefile-only root", project.Commands)
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

func TestDetectFillsNodeAndGoProjectRoots(t *testing.T) {
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
	if backend.Path != "backend" {
		t.Fatalf("backend path = %q", backend.Path)
	}
	if len(backend.Languages) != 1 || backend.Languages[0].Name != "go" {
		t.Fatalf("backend languages = %+v, want go", backend.Languages)
	}
	if len(backend.Commands) == 0 || len(backend.Preparation) == 0 {
		t.Fatalf("backend = %+v, want Go inferred commands", backend)
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

func TestDetectIsDeterministicForEquivalentRuntimePins(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{"name":"app"}`+"\n")
	writeFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"), `
jobs:
  zebra:
    steps:
      - uses: actions/setup-node@v4
        with:
          node-version: 18.x
      - run: npm test
  alpha:
    steps:
      - uses: actions/setup-node@v4
        with:
          node-version: "18"
      - run: npm test
`)

	first, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	second, err := Detect(root)
	if err != nil {
		t.Fatalf("second Detect() error = %v", err)
	}
	got, err := first.MarshalCanonical()
	if err != nil {
		t.Fatalf("MarshalCanonical() error = %v", err)
	}
	again, err := second.MarshalCanonical()
	if err != nil {
		t.Fatalf("second MarshalCanonical() error = %v", err)
	}
	if !bytes.Equal(got, again) {
		t.Fatalf("Detect() was not deterministic\n first:\n%s\nsecond:\n%s", got, again)
	}
}

func TestDetectAssemblesMakeComposeAndEnvRequirements(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/app\n\ngo 1.26\n")
	writeFile(t, filepath.Join(root, "main_test.go"), "package main\n")
	writeFile(t, filepath.Join(root, "Makefile"), ""+
		".PHONY: test install\n"+
		"\n"+
		"install:\n"+
		"\tgo mod download\n"+
		"\n"+
		"test:\n"+
		"\tgo test ./...\n")
	writeFile(t, filepath.Join(root, "compose.yaml"), ""+
		"services:\n"+
		"  postgres:\n"+
		"    image: postgres:16\n"+
		"    environment:\n"+
		"      DATABASE_URL: postgres://app@postgres/app\n")
	writeFile(t, filepath.Join(root, ".env.example"), "DATABASE_URL=\nAPI_SECRET=changeme\n")

	document, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if len(document.Projects) != 1 {
		t.Fatalf("len(projects) = %d, want 1", len(document.Projects))
	}
	project := document.Projects[0]

	if !hasRequirement(project, plan.RequirementService, "postgres", "16") {
		t.Fatalf("requirements = %+v, want postgres 16", project.Requirements)
	}
	if !hasEnv(project, "DATABASE_URL", true, true) {
		t.Fatalf("DATABASE_URL = %+v, want required with a default from compose", project.Requirements)
	}
	if !hasEnv(project, "API_SECRET", true, true) {
		t.Fatalf("API_SECRET = %+v, want required with a default from .env.example", project.Requirements)
	}

	var sawMakeTest, sawComposeUp, sawMakeInstall bool
	for _, command := range project.Commands {
		if command.Name == "test" && derefRun(command.Run) == "make test" {
			sawMakeTest = true
		}
	}
	for _, command := range project.Preparation {
		if derefRun(command.Run) == "docker compose up -d" {
			sawComposeUp = true
		}
		if command.Name == "install" && derefRun(command.Run) == "make install" {
			sawMakeInstall = true
		}
	}
	if !sawMakeTest {
		t.Fatalf("commands = %+v, want make test", project.Commands)
	}
	if !sawComposeUp || !sawMakeInstall {
		t.Fatalf("preparation = %+v, want make install and docker compose up -d", project.Preparation)
	}
	for _, command := range append(append([]plan.Command{}, project.Commands...), project.Preparation...) {
		if command.Origin == plan.CommandInferred && derefRun(command.Run) == "go test ./..." {
			t.Fatalf("inferred go test remained beside declared make test: %+v", command)
		}
		if command.Origin == plan.CommandInferred && derefRun(command.Run) == "go mod download" {
			t.Fatalf("inferred go mod download remained beside declared make install: %+v", command)
		}
	}
}

func TestDetectPutsDeclaredInstallScriptsInPreparation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "package.json"), `{"scripts":{"setup":"npm ci","test":"vitest"}}`)
	writeFile(t, filepath.Join(root, "package-lock.json"), "{}\n")

	document, err := Detect(root)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if len(document.Projects) != 1 {
		t.Fatalf("len(projects) = %d, want 1", len(document.Projects))
	}
	project := document.Projects[0]

	var sawSetup bool
	for _, command := range project.Preparation {
		if command.Name == "setup" && derefRun(command.Run) == "npm run setup" {
			sawSetup = true
		}
		if command.Origin == plan.CommandInferred && derefRun(command.Run) == "npm install" {
			t.Fatalf("inferred npm install remained beside declared setup: %+v", command)
		}
	}
	if !sawSetup {
		t.Fatalf("preparation = %+v, want declared npm run setup (install-capable)", project.Preparation)
	}
	for _, command := range project.Commands {
		if command.Name == "setup" {
			t.Fatalf("install-capable setup stayed in commands: %+v", command)
		}
		for _, interpretation := range command.Interpretations {
			if interpretation.Capability == plan.CapabilityDependenciesInstall {
				t.Fatalf("install-capable command stayed in commands: %+v", command)
			}
		}
	}
}

func hasRequirement(project plan.ProjectPlan, kind plan.RequirementKind, name, version string) bool {
	for _, requirement := range project.Requirements {
		if requirement.Kind == kind && requirement.Name == name && requirement.Version == version {
			return true
		}
	}
	return false
}

func hasEnv(project plan.ProjectPlan, name string, required, hasDefault bool) bool {
	for _, requirement := range project.Requirements {
		if requirement.Kind != plan.RequirementEnvironment || requirement.Name != name {
			continue
		}
		gotRequired := requirement.IsRequired != nil && *requirement.IsRequired
		gotDefault := requirement.HasDefault != nil && *requirement.HasDefault
		return gotRequired == required && gotDefault == hasDefault
	}
	return false
}

func derefRun(value *string) string {
	if value == nil {
		return ""
	}
	return *value
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
