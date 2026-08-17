package gha

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

func TestDetectReturnsNothingWithoutWorkflows(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{"README.md": "hello\n"})
	if len(result.Findings) != 0 {
		t.Fatalf("Detect() = %+v, want no findings", result)
	}
}

func TestDetectReadsJobsStepsMatrixServicesAndEnv(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".nvmrc": "22\n",
		".github/workflows/ci.yml": `
name: CI
on: [push]
env:
  APP_URL: http://localhost
  API_TOKEN: ${{ secrets.API_TOKEN }}
  CI: true
  PR_NUMBER: ${{ github.event.number }}
jobs:
  test:
    runs-on: ubuntu-latest
    services:
      redis:
        image: redis:7
    strategy:
      matrix:
        node-version: [22, 24]
    defaults:
      run:
        working-directory: frontend
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: ${{ matrix.node-version }}
      - run: |
          npm ci
          npm test -- --run
        env:
          NODE_ENV: test
          TZ: UTC
      - run: cd frontend && echo skip-me
      - run: node scripts/check-generated.mjs
`,
	})

	commands := commandByName(result)
	if deref(commands["install dependencies"].Run) != "npm ci" {
		t.Fatalf("install = %+v, want npm ci", commands["install dependencies"])
	}
	if commands["install dependencies"].Directory != "frontend" {
		t.Fatalf("install directory = %q, want frontend", commands["install dependencies"].Directory)
	}
	if deref(commands["test"].Run) != "npm test -- --run" {
		t.Fatalf("test = %+v, want npm test -- --run", commands["test"])
	}
	if deref(commands["node scripts/check-generated.mjs"].Run) != "node scripts/check-generated.mjs" {
		t.Fatalf("unlinked command = %+v", commands["node scripts/check-generated.mjs"])
	}

	versions := requirementVersions(result, plan.RequirementRuntime, "node")
	if !slices.Equal(sortedCopy(versions), []string{"22", "24"}) {
		t.Fatalf("node versions = %v, want 22 and 24", versions)
	}

	if !hasRequirement(result, plan.RequirementService, "redis", "7") {
		t.Fatalf("missing redis 7 service in %+v", result.Findings)
	}
	if !hasEnv(result, "APP_URL", false, true) {
		t.Fatalf("missing APP_URL default in %+v", result.Findings)
	}
	if !hasEnv(result, "API_TOKEN", true, false) {
		t.Fatalf("missing secret API_TOKEN in %+v", result.Findings)
	}
	if hasEnvName(result, "CI") {
		t.Fatalf("CI should be skipped, got %+v", result.Findings)
	}
	if hasEnvName(result, "PR_NUMBER") {
		t.Fatalf("GitHub context env should be skipped, got %+v", result.Findings)
	}
	if hasEnvName(result, "TZ") {
		t.Fatalf("step-level literal env should be skipped, got %+v", result.Findings)
	}
}

func TestDetectRecordsReusableWorkflowLimitation(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/call.yml": `
jobs:
  reuse:
    uses: ./.github/workflows/ci.yml
`,
	})

	values := factValues(result, "provider.github-actions.limitation")
	if !slices.Contains(values, "reusable-workflows") {
		t.Fatalf("facts = %v, want reusable-workflows", values)
	}
}

func TestDetectRecordsLocalCompositeActionLimitation(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  build:
    steps:
      - uses: ./.github/actions/setup
`,
	})

	values := factValues(result, "provider.github-actions.limitation")
	if !slices.Contains(values, "composite-actions") {
		t.Fatalf("facts = %v, want composite-actions", values)
	}
}

func TestDetectAppliesYarnCwdToCommandDirectory(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - run: yarn --cwd ./packages/app test --watch=false
`,
	})

	commands := commandByName(result)
	got := commands["test"]
	if got.Directory != "packages/app" {
		t.Fatalf("directory = %q, want packages/app", got.Directory)
	}
	if deref(got.Run) != "yarn --cwd ./packages/app test --watch=false" {
		t.Fatalf("run = %q, want the original invocation", deref(got.Run))
	}
}

func TestDetectReadsNodeVersionFile(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".nvmrc": "20\n",
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - uses: actions/setup-node@v4
        with:
          node-version-file: .nvmrc
`,
	})

	if !hasRequirement(result, plan.RequirementRuntime, "node", "20") {
		t.Fatalf("missing node 20 from version file in %+v", result.Findings)
	}
}

func TestDetectSkipsCommentsAndContinue(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - run: |
          # into K buckets so each upload's finalize body is small
          # stay a disjoint partition
          pnpm test
          continue
          ! grep -q skip changed.txt
          pnpm lint
`,
	})

	commands := commandByName(result)
	if got := keys(commands); !slices.Equal(got, []string{"lint", "test"}) {
		t.Fatalf("commands = %v, want lint and test", got)
	}
	if deref(commands["test"].Run) != "pnpm test" {
		t.Fatalf("test run = %q, want pnpm test", deref(commands["test"].Run))
	}
	if deref(commands["lint"].Run) != "pnpm lint" {
		t.Fatalf("lint run = %q, want pnpm lint", deref(commands["lint"].Run))
	}
}

func TestDetectSkipsPlumbingAndGlobalInstalls(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - run: |
          mkdir -p cypress/argos-baseline
          cp -r cypress/screenshots/. cypress/argos-baseline/
          git fetch origin develop --depth=1
          npm install -g npm@11
          pnpm test
`,
	})

	if got := keys(commandByName(result)); !slices.Equal(got, []string{"test"}) {
		t.Fatalf("commands = %v, want only test", got)
	}
}

func TestDetectSkipsShellControlFlow(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - run: |
          set -euo pipefail
          yarn locales-coverage
`,
	})

	commands := commandByName(result)
	if _, ok := commands["set"]; ok {
		t.Fatalf("commands = %v, did not want set", keys(commands))
	}
	if _, ok := commands["locales-coverage"]; !ok {
		t.Fatalf("commands = %v, want locales-coverage", keys(commands))
	}
}

func TestDetectCollapsesHeredocsAndLineContinuations(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - run: |
          gh api \
            --method DELETE \
            /repos/example
          node <<'NODE'
          const title = process.env.PR_TITLE
          console.log(title)
          NODE
          yarn test
`,
	})

	commands := commandByName(result)
	if _, ok := commands["const"]; ok {
		t.Fatalf("commands = %v, did not want heredoc body treated as commands", keys(commands))
	}
	if _, ok := commands["test"]; !ok {
		t.Fatalf("commands = %v, want yarn test", keys(commands))
	}

	runs := commandRunTexts(result)
	if !containsPrefix(runs, "gh api") {
		t.Fatalf("runs = %v, want joined gh api", runs)
	}
	if !containsPrefix(runs, "node") {
		t.Fatalf("runs = %v, want node heredoc invocation", runs)
	}
}

func commandRunTexts(result provider.Result) []string {
	var runs []string
	for _, finding := range result.Findings {
		item, ok := finding.(plan.CommandFinding)
		if !ok || item.Command.Run == nil {
			continue
		}
		runs = append(runs, *item.Command.Run)
	}
	return runs
}

func containsPrefix(values []string, prefix string) bool {
	for _, value := range values {
		if strings.HasPrefix(strings.TrimSpace(value), prefix) {
			return true
		}
	}
	return false
}

func detectFiles(t *testing.T, files map[string]string) provider.Result {
	t.Helper()

	root := t.TempDir()
	for name, contents := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("os.MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatalf("os.WriteFile() error = %v", err)
		}
	}

	result, err := Provider{}.Detect(provider.Context{RepositoryRoot: root, ProjectPath: "."})
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	return result
}

func commandByName(result provider.Result) map[string]plan.Command {
	out := make(map[string]plan.Command)
	for _, finding := range result.Findings {
		item, ok := finding.(plan.CommandFinding)
		if !ok {
			continue
		}
		out[item.Command.Name] = item.Command
	}
	return out
}

func requirementVersions(result provider.Result, kind plan.RequirementKind, name string) []string {
	var versions []string
	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if !ok || item.Requirement.Kind != kind || item.Requirement.Name != name {
			continue
		}
		versions = append(versions, item.Requirement.Version)
	}
	return versions
}

func hasRequirement(result provider.Result, kind plan.RequirementKind, name, version string) bool {
	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if !ok {
			continue
		}
		if item.Requirement.Kind == kind && item.Requirement.Name == name && item.Requirement.Version == version {
			return true
		}
	}
	return false
}

func hasEnv(result provider.Result, name string, required, hasDefault bool) bool {
	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if !ok || item.Requirement.Kind != plan.RequirementEnvironment || item.Requirement.Name != name {
			continue
		}
		if item.Requirement.IsRequired == nil || item.Requirement.HasDefault == nil {
			return false
		}
		return *item.Requirement.IsRequired == required && *item.Requirement.HasDefault == hasDefault
	}
	return false
}

func hasEnvName(result provider.Result, name string) bool {
	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if ok && item.Requirement.Kind == plan.RequirementEnvironment && item.Requirement.Name == name {
			return true
		}
	}
	return false
}

func factValues(result provider.Result, name string) []string {
	var values []string
	for _, finding := range result.Findings {
		item, ok := finding.(plan.PropertyFinding)
		if !ok || item.Property.Kind != plan.PropertyFact || item.Property.Name != name {
			continue
		}
		values = append(values, item.Property.Value)
	}
	return values
}

func keys(commands map[string]plan.Command) []string {
	out := make([]string, 0, len(commands))
	for name := range commands {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

func sortedCopy(values []string) []string {
	out := append([]string{}, values...)
	slices.Sort(out)
	return out
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
