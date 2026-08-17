package gha

import (
	"fmt"
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

func TestDetectEmitsGolangciLintActionAsACommand(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/lint.yml": `
jobs:
  lint:
    steps:
      - uses: golangci/golangci-lint-action@v8
        with:
          version: latest
          args: --verbose
`,
	})

	commands := commandByName(result)
	got := commands["golangci-lint run"]
	if deref(got.Run) != "golangci-lint run --verbose" {
		t.Fatalf("run = %q, want golangci-lint run --verbose", deref(got.Run))
	}
	if !commandHasCapability(got, plan.CapabilityCodeLint) {
		t.Fatalf("interpretations = %+v, want code.lint", got.Interpretations)
	}
}

func TestDetectSkipsRemoteGoInstallAndGoPlumbing(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - run: |
          go version
          go env
          go install github.com/kyoh86/richgo@latest
          go test ./...
`,
	})

	if got := keys(commandByName(result)); !slices.Equal(got, []string{"go test"}) {
		t.Fatalf("commands = %v, want only go test", got)
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
	for _, run := range runs {
		if strings.Contains(run, "<<") {
			t.Fatalf("runs = %v, did not want a heredoc invocation", runs)
		}
	}
}

func TestDetectIsDeterministicAcrossEquivalentJobs(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		".github/workflows/ci.yml": `
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
`,
	}
	first := detectFiles(t, files)
	second := detectFiles(t, files)
	if findingDump(first) != findingDump(second) {
		t.Fatalf("Detect() findings were not deterministic\n first:\n%s\nsecond:\n%s", findingDump(first), findingDump(second))
	}
}

func TestDetectLeavesUnresolvedSetupExpressionsUnversioned(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - uses: actions/setup-node@v4
        with:
          node-version: ${{ vars.NODE_VERSION }}
`,
	})
	if !hasRequirement(result, plan.RequirementRuntime, "node", "") {
		t.Fatalf("missing unversioned node runtime in %+v", result.Findings)
	}
	if hasRequirement(result, plan.RequirementRuntime, "node", "${{ vars.NODE_VERSION }}") {
		t.Fatalf("expression was stored as a version in %+v", result.Findings)
	}
}

func TestDetectDoesNotLeakDirectoryFlagsAcrossStatements(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - run: |
          npm --prefix frontend test
          npm --prefix backend test
`,
	})
	var dirs []string
	for _, finding := range result.Findings {
		item, ok := finding.(plan.CommandFinding)
		if !ok {
			continue
		}
		dirs = append(dirs, item.Command.Directory)
	}
	if !slices.Equal(sortedCopy(dirs), []string{"backend", "frontend"}) {
		t.Fatalf("directories = %v, want frontend and backend", dirs)
	}
}

func TestDetectDoesNotTreatDoubleDashPrefixAsADirectory(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - run: npm test -- --prefix fixtures
`,
	})
	commands := commandByName(result)
	got := commands["test"]
	if got.Directory != "." {
		t.Fatalf("directory = %q, want .", got.Directory)
	}
}

func TestDetectOmitsMissingVersionFileEvidence(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - uses: actions/setup-node@v4
        with:
          node-version-file: .nvmrc
`,
	})
	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if !ok || item.Requirement.Name != "node" {
			continue
		}
		for _, evidence := range item.Requirement.Evidence {
			if evidence.Kind == plan.EvidenceFile {
				t.Fatalf("evidence = %+v, did not want a file citation for a missing .nvmrc", item.Requirement.Evidence)
			}
		}
		return
	}
	t.Fatal("missing node runtime requirement")
}

func TestDetectParsesRegistryPortImageTags(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    services:
      db:
        image: registry.example.com:5000/postgres:16
    steps:
      - run: echo skip
`,
	})
	if !hasRequirement(result, plan.RequirementService, "postgres", "16") {
		t.Fatalf("missing postgres 16 in %+v", result.Findings)
	}
}

func TestDetectIgnoresMatrixExcludeEntries(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    strategy:
      matrix:
        node-version: [18]
        exclude:
          - node-version: 20
    steps:
      - uses: actions/setup-node@v4
        with:
          node-version: ${{ matrix.node-version }}
`,
	})
	versions := requirementVersions(result, plan.RequirementRuntime, "node")
	if !slices.Equal(versions, []string{"18"}) {
		t.Fatalf("node versions = %v, want only 18", versions)
	}
}

func TestDetectRedactsInlineAssignmentValues(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - run: API_TOKEN=hunter2 npm test
`,
	})
	commands := commandByName(result)
	got := deref(commands["test"].Run)
	if got != "API_TOKEN=$API_TOKEN npm test" {
		t.Fatalf("run = %q, want redacted assignment", got)
	}
	if strings.Contains(got, "hunter2") {
		t.Fatalf("run = %q, leaked a literal assignment value", got)
	}
}

func TestDetectExpandsMatrixWorkingDirectory(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    strategy:
      matrix:
        package: [packages/app, packages/lib]
    defaults:
      run:
        working-directory: ${{ matrix.package }}
    steps:
      - run: npm test
`,
	})
	dirs := commandDirectories(result, "test")
	if !slices.Equal(sortedCopy(dirs), []string{"packages/app", "packages/lib"}) {
		t.Fatalf("directories = %v, want packages/app and packages/lib", dirs)
	}
	for _, dir := range dirs {
		if strings.Contains(dir, "${{") {
			t.Fatalf("directories = %v, did not want an unresolved expression", dirs)
		}
	}
}

func TestDetectLeavesUnresolvedWorkingDirectoryOnTheParent(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    defaults:
      run:
        working-directory: ${{ inputs.dir }}
    steps:
      - run: npm test
`,
	})
	dirs := commandDirectories(result, "test")
	if !slices.Equal(dirs, []string{"."}) {
		t.Fatalf("directories = %v, want the parent project", dirs)
	}
}

func TestDetectKeepsSecretEnvWhenALaterExpressionIsASecret(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
env:
  BUILD_TAG: prefix-${{ github.ref }}-${{ secrets.API_TOKEN }}
jobs:
  test:
    steps:
      - run: echo skip
`,
	})
	if !hasEnv(result, "BUILD_TAG", true, false) {
		t.Fatalf("missing secret BUILD_TAG in %+v", result.Findings)
	}
}

func TestDetectSkipsCurlInstallers(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - run: |
          curl -sL https://sentry.io/get-cli/ | bash
          npm test
`,
	})
	if got := keys(commandByName(result)); !slices.Equal(got, []string{"test"}) {
		t.Fatalf("commands = %v, want only test", got)
	}
}

func TestDetectDoesNotTreatSecretsPathAsASecretExpression(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
env:
  CONFIG: config/secrets.yaml
  API_TOKEN: ${{ secrets.API_TOKEN }}
jobs:
  test:
    steps:
      - run: echo skip
`,
	})
	if !hasEnv(result, "API_TOKEN", true, false) {
		t.Fatalf("missing secret API_TOKEN in %+v", result.Findings)
	}
	if hasEnv(result, "CONFIG", true, false) {
		t.Fatalf("CONFIG should not be secret-supplied, got %+v", result.Findings)
	}
}

func findingDump(result provider.Result) string {
	var b strings.Builder
	for _, finding := range result.Findings {
		switch item := finding.(type) {
		case plan.CommandFinding:
			fmt.Fprintf(&b, "cmd %s %s %s %s\n", item.Command.Name, deref(item.Command.Run), item.Command.Directory, item.Command.Evidence[0].Pointer)
		case plan.RequirementFinding:
			fmt.Fprintf(&b, "req %s %s %s\n", item.Requirement.Name, item.Requirement.Version, item.Requirement.Evidence[0].Pointer)
		case plan.PropertyFinding:
			fmt.Fprintf(&b, "prop %s %s\n", item.Property.Name, item.Property.Value)
		}
	}
	return b.String()
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

func commandDirectories(result provider.Result, name string) []string {
	var dirs []string
	for _, finding := range result.Findings {
		item, ok := finding.(plan.CommandFinding)
		if !ok || item.Command.Name != name {
			continue
		}
		dirs = append(dirs, item.Command.Directory)
	}
	return dirs
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

func commandHasCapability(command plan.Command, capability plan.Capability) bool {
	for _, interpretation := range command.Interpretations {
		if interpretation.Capability == capability {
			return true
		}
	}
	return false
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
