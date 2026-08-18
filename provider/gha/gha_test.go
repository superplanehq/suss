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

	versions := runtimeRequirementVersions(result, "node")
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

func TestDetectRewritesCargoDirectoryFlags(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"crates/tool/Cargo.toml": "[package]\nname = \"tool\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - run: cargo test --manifest-path crates/tool/Cargo.toml --locked
      - run: cargo -C crates/tool build
`,
	})

	commands := commandByName(result)
	testCmd := commands["cargo test"]
	if testCmd.Directory != "." {
		t.Fatalf("test directory = %q, want . so Cargo still reads the parent .cargo config", testCmd.Directory)
	}
	if deref(testCmd.Run) != "cargo test --manifest-path crates/tool/Cargo.toml --locked" {
		t.Fatalf("test run = %q, want the original manifest-path invocation", deref(testCmd.Run))
	}
	if !commandHasCapability(testCmd, plan.CapabilityTestRun) {
		t.Fatalf("test interpretations = %+v, want test.run", testCmd.Interpretations)
	}
	buildCmd := commands["cargo build"]
	if buildCmd.Directory != "crates/tool" {
		t.Fatalf("build directory = %q, want crates/tool", buildCmd.Directory)
	}
	if deref(buildCmd.Run) != "cargo build" {
		t.Fatalf("build run = %q, want cargo build without -C", deref(buildCmd.Run))
	}
}

func TestDetectPreservesCargoCWithShellRedirects(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"crate/Cargo.toml": "[package]\nname = \"crate\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - run: cargo -C crate test > result.log
`,
	})

	got := commandByName(result)["cargo test"]
	if got.Directory != "." {
		t.Fatalf("directory = %q, want . so result.log stays at the repository root", got.Directory)
	}
	if deref(got.Run) != "cargo -C crate test > result.log" {
		t.Fatalf("run = %q, want the original -C redirect invocation", deref(got.Run))
	}
}

func TestDetectComposesCargoDirectoryAndManifestPath(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"crates/tool/Cargo.toml": "[package]\nname = \"tool\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - run: cargo -C crates/tool test --manifest-path Cargo.toml
`,
	})

	commands := commandByName(result)
	testCmd := commands["cargo test"]
	if testCmd.Directory != "crates/tool" {
		t.Fatalf("directory = %q, want crates/tool from -C plus a relative manifest path", testCmd.Directory)
	}
	if deref(testCmd.Run) != "cargo test --manifest-path Cargo.toml" {
		t.Fatalf("run = %q, want cargo test with the manifest path kept after stripping -C", deref(testCmd.Run))
	}
}

func TestDetectRewritesCargoDirectoryFlagsThroughRustup(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"crates/tool/Cargo.toml": "[package]\nname = \"tool\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - run: rustup run nightly cargo test --manifest-path crates/tool/Cargo.toml
`,
	})

	got := commandByName(result)["cargo test"]
	if got.Directory != "." {
		t.Fatalf("directory = %q, want . so Cargo still reads the parent .cargo config", got.Directory)
	}
	if deref(got.Run) != "rustup run nightly cargo test --manifest-path crates/tool/Cargo.toml" {
		t.Fatalf("run = %q, want the original rustup manifest-path invocation", deref(got.Run))
	}
}

func TestDetectLeavesDynamicCargoManifestPathUnresolved(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"crates/tool/Cargo.toml": "[package]\nname = \"tool\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - run: cargo test --manifest-path $SEMAPHORE_GIT_DIR/crates/tool/Cargo.toml
`,
	})

	got := commandByName(result)["cargo test"]
	if got.Directory != "." {
		t.Fatalf("directory = %q, want . for a variable-valued manifest path", got.Directory)
	}
	if deref(got.Run) != "cargo test --manifest-path $SEMAPHORE_GIT_DIR/crates/tool/Cargo.toml" {
		t.Fatalf("run = %q, want the original command", deref(got.Run))
	}
}

func TestDetectRetainsVerbosePythonManagerInstalls(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - run: pip -v install -r requirements.txt
      - run: uv -v sync
      - run: poetry -v install
`,
	})

	found := map[string]bool{}
	for _, finding := range result.Findings {
		item, ok := finding.(plan.CommandFinding)
		if !ok || item.Command.Run == nil {
			continue
		}
		found[*item.Command.Run] = true
	}
	for _, run := range []string{"pip -v install -r requirements.txt", "uv -v sync", "poetry -v install"} {
		if !found[run] {
			t.Fatalf("missing %q in %+v", run, result.Findings)
		}
	}
}

func TestDetectAppliesUvDirectoryToCommandDirectory(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - run: uv run --directory packages/api pytest
      - run: uv run -C packages/web pytest
      - run: uv --directory packages/cli run pytest
`,
	})

	found := map[string]string{}
	for _, finding := range result.Findings {
		item, ok := finding.(plan.CommandFinding)
		if !ok || item.Command.Run == nil {
			continue
		}
		found[*item.Command.Run] = item.Command.Directory
	}
	if got := found["uv run --directory packages/api pytest"]; got != "packages/api" {
		t.Fatalf("uv --directory directory = %q, want packages/api in %+v", got, found)
	}
	if got := found["uv run -C packages/web pytest"]; got != "packages/web" {
		t.Fatalf("uv -C directory = %q, want packages/web in %+v", got, found)
	}
	if got := found["uv --directory packages/cli run pytest"]; got != "packages/cli" {
		t.Fatalf("uv global --directory directory = %q, want packages/cli in %+v", got, found)
	}
	interpreted := false
	for _, finding := range result.Findings {
		item, ok := finding.(plan.CommandFinding)
		if !ok || item.Command.Run == nil || *item.Command.Run != "uv --directory packages/cli run pytest" {
			continue
		}
		interpreted = commandHasCapability(item.Command, plan.CapabilityTestRun)
	}
	if !interpreted {
		t.Fatal("uv --directory packages/cli run pytest was not interpreted as a test command")
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

func TestDetectReadsSetupBeamMatrixVersions(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    strategy:
      matrix:
        include:
          - elixir: "1.18.1"
            otp: "27.2"
          - elixir: "1.17.3"
            otp: "26.2"
    steps:
      - uses: erlef/setup-beam@v1
        with:
          elixir-version: ${{ matrix.elixir }}
          otp-version: ${{ matrix.otp }}
`,
	})

	if got := sortedCopy(runtimeRequirementVersions(result, "elixir")); !slices.Equal(got, []string{"1.17.3", "1.18.1"}) {
		t.Fatalf("Elixir versions = %v, want matrix pins", got)
	}
	if got := sortedCopy(runtimeRequirementVersions(result, "erlang")); !slices.Equal(got, []string{"26.2", "27.2"}) {
		t.Fatalf("Erlang versions = %v, want matrix pins", got)
	}
}

func TestDetectReadsSetupRubyMatrixVersions(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    strategy:
      matrix:
        ruby: ["3.3", "3.4"]
    steps:
      - uses: ruby/setup-ruby@v1
        with:
          ruby-version: ${{ matrix.ruby }}
`,
	})

	if got := sortedCopy(runtimeRequirementVersions(result, "ruby")); !slices.Equal(got, []string{"3.3", "3.4"}) {
		t.Fatalf("Ruby versions = %v, want matrix pins", got)
	}
}

func TestDetectReadsSetupRubyVersionFileInput(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".ruby-version": "3.4.5\n",
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - uses: ruby/setup-ruby@v1
        with:
          ruby-version: .ruby-version
`,
	})

	if !hasRequirement(result, plan.RequirementRuntime, "ruby", "3.4.5") {
		t.Fatalf("missing Ruby 3.4.5 from .ruby-version in %+v", result.Findings)
	}
}

func TestDetectReadsSetupRubyVersionFileEngineAliases(t *testing.T) {
	t.Parallel()

	for _, version := range []string{"ruby-3.3.0", "jruby-9.4.8.0", "truffleruby-24.1.0", "ruby-head"} {
		result := detectFiles(t, map[string]string{
			".ruby-version": version + "\n",
			".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - uses: ruby/setup-ruby@v1
        with:
          ruby-version: .ruby-version
`,
		})
		if !hasRequirement(result, plan.RequirementRuntime, "ruby", version) {
			t.Fatalf("missing Ruby %q from .ruby-version in %+v", version, result.Findings)
		}
	}
}

func TestDetectReadsSetupPHPMatrixVersions(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    strategy:
      matrix:
        php: ["8.3", "8.4"]
    steps:
      - uses: shivammathur/setup-php@v2
        with:
          php-version: ${{ matrix.php }}
`,
	})

	if got := sortedCopy(runtimeRequirementVersions(result, "php")); !slices.Equal(got, []string{"8.3", "8.4"}) {
		t.Fatalf("PHP versions = %v, want matrix pins", got)
	}
}

func TestDetectPointsUnpinnedSetupPHPAtUses(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - uses: shivammathur/setup-php@v2
`,
	})

	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if !ok || item.Requirement.Name != "php" {
			continue
		}
		if item.Requirement.Version != "" {
			t.Fatalf("unpinned setup-php version = %q, want empty", item.Requirement.Version)
		}
		if len(item.Requirement.Evidence) == 0 || item.Requirement.Evidence[0].Pointer != "/jobs/test/steps/0/uses" {
			t.Fatalf("unpinned setup-php evidence = %+v, want /jobs/test/steps/0/uses", item.Requirement.Evidence)
		}
		return
	}
	t.Fatal("missing unpinned PHP runtime from setup-php")
}

func TestDetectIgnoresUnknownSetupPHPVersionFileInput(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".php-version": "8.3.6\n",
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - uses: shivammathur/setup-php@v2
        with:
          php-version-file: .php-version
`,
	})

	if hasRequirement(result, plan.RequirementRuntime, "php", "8.3.6") {
		t.Fatal("setup-php php-version-file input was treated as a supported version pin")
	}
}

func TestDetectReadsRustToolchainActionTag(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - uses: dtolnay/rust-toolchain@1.81.0
      - run: cargo test
`,
	})

	if !hasRequirement(result, plan.RequirementRuntime, "rust", "1.81.0") {
		t.Fatalf("missing rust 1.81.0 from action tag in %+v", result.Findings)
	}
	if commands := commandByName(result); deref(commands["cargo test"].Run) != "cargo test" {
		t.Fatalf("commands = %+v, want cargo test", commands)
	}
}

func TestDetectReadsRustToolchainFileAndSkipsRemoteInstalls(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"rust-toolchain.toml": "[toolchain]\nchannel = \"1.80.0\"\n",
		".github/workflows/ci.yml": `
jobs:
  test:
    strategy:
      matrix:
        rust: ["1.80.0", "1.81.0"]
    steps:
      - uses: dtolnay/rust-toolchain@master
        with:
          toolchain: ${{ matrix.rust }}
      - run: rustup component add clippy
      - run: cargo install cargo-nextest
      - run: cargo test --locked
`,
	})

	if got := sortedCopy(runtimeRequirementVersions(result, "rust")); !slices.Equal(got, []string{"1.80.0", "1.81.0"}) {
		t.Fatalf("Rust versions = %v, want matrix pins", got)
	}
	commands := commandByName(result)
	if deref(commands["cargo test"].Run) != "cargo test --locked" {
		t.Fatalf("commands = %+v, want cargo test --locked", commands)
	}
	if _, ok := commands["rustup component add"]; ok {
		t.Fatalf("rustup was emitted as a repository command: %+v", result.Findings)
	}
	if _, ok := commands["cargo install"]; ok {
		t.Fatalf("remote cargo install was emitted as a repository command: %+v", result.Findings)
	}
}

func TestDetectRejectsRustToolchainActionSHA(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - uses: dtolnay/rust-toolchain@0123456789abcdef0123456789abcdef01234567
      - run: rustup run nightly cargo test
      - run: rustc -V
      - run: rustc -vV
`,
	})

	versions := runtimeRequirementVersions(result, "rust")
	if len(versions) != 1 || versions[0] != "" {
		t.Fatalf("rust versions = %v, want one unversioned requirement", versions)
	}
	commands := commandByName(result)
	if deref(commands["cargo test"].Run) != "rustup run nightly cargo test" {
		t.Fatalf("commands = %+v, want rustup run nightly cargo test kept as cargo test", commands)
	}
	if _, ok := commands["rustc"]; ok {
		t.Fatalf("rustc version probes were emitted as repository commands: %+v", result.Findings)
	}
}

func TestDetectUsesSetupRustToolchainStableDefault(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"rust-toolchain.toml": "[toolchain]\nchannel = \"1.80.0\"\n",
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - uses: actions-rust-lang/setup-rust-toolchain@v1
`,
	})

	versions := runtimeRequirementVersions(result, "rust")
	if !slices.Contains(versions, "stable") {
		t.Fatalf("rust versions = %v, want the action default stable", versions)
	}
	if slices.Contains(versions, "1.80.0") {
		t.Fatalf("rust versions = %v, did not want the repository toolchain file", versions)
	}
}

func TestDetectExpandsMatrixToolchainFile(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"rust-toolchain.toml": "[toolchain]\nchannel = \"1.80.0\"\n",
		"ci/msrv.toml":        "[toolchain]\nchannel = \"1.74.0\"\n",
		"ci/current.toml":     "[toolchain]\nchannel = \"1.81.0\"\n",
		".github/workflows/ci.yml": `
jobs:
  test:
    strategy:
      matrix:
        toolchain_file: ["ci/msrv.toml", "ci/current.toml"]
    steps:
      - uses: actions-rust-lang/setup-rust-toolchain@v1
        with:
          toolchain-file: ${{ matrix.toolchain_file }}
`,
	})

	got := sortedCopy(runtimeRequirementVersions(result, "rust"))
	if !slices.Equal(got, []string{"1.74.0", "1.81.0"}) {
		t.Fatalf("rust versions = %v, want matrix toolchain files", got)
	}
	if slices.Contains(got, "1.80.0") {
		t.Fatalf("rust versions = %v, did not want the default rust-toolchain.toml", got)
	}
}

func TestDetectLeavesUnresolvedToolchainFileExpression(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"rust-toolchain.toml": "[toolchain]\nchannel = \"1.80.0\"\n",
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - uses: actions-rust-lang/setup-rust-toolchain@v1
        with:
          toolchain-file: ${{ vars.TOOLCHAIN_FILE }}
`,
	})

	versions := runtimeRequirementVersions(result, "rust")
	if len(versions) != 1 || versions[0] != "" {
		t.Fatalf("rust versions = %v, want one unresolved requirement", versions)
	}
}

func TestDetectReadsRustToolchainFileInput(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"rust-toolchain.toml": "[toolchain]\nchannel = \"stable\"\n",
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - uses: actions-rust-lang/setup-rust-toolchain@v1
        with:
          toolchain-file: rust-toolchain.toml
`,
	})

	if !hasRequirement(result, plan.RequirementRuntime, "rust", "stable") {
		t.Fatalf("missing rust stable from toolchain file in %+v", result.Findings)
	}
}

func TestDetectReadsSetupPythonMatrixVersions(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    strategy:
      matrix:
        python: ["3.12", "3.13"]
    steps:
      - uses: actions/setup-python@v5
        with:
          python-version: ${{ matrix.python }}
`,
	})

	if got := sortedCopy(runtimeRequirementVersions(result, "python")); !slices.Equal(got, []string{"3.12", "3.13"}) {
		t.Fatalf("Python versions = %v, want matrix pins", got)
	}
}


func TestDetectIgnoresPyprojectTomlAsPythonVersionFile(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml": "[project]\nrequires-python = \">=3.10\"\n",
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - uses: actions/setup-python@v5
        with:
          python-version-file: pyproject.toml
`,
	})

	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if !ok || item.Requirement.Name != "python" {
			continue
		}
		if item.Requirement.Version == "[project]" {
			t.Fatalf("treated pyproject.toml table header as a Python version: %+v", item.Requirement)
		}
	}
}


func TestDetectReadsSetupPythonVersionFileInput(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".python-version": "3.12.8\n",
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - uses: actions/setup-python@v5
        with:
          python-version-file: .python-version
`,
	})

	if !hasRequirement(result, plan.RequirementRuntime, "python", "3.12.8") {
		t.Fatalf("missing Python 3.12.8 from .python-version in %+v", result.Findings)
	}
}

func TestDetectReadsSetupPythonGraalPyVersionFile(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".python-version": "graalpy-24.1\n",
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - uses: actions/setup-python@v5
        with:
          python-version-file: .python-version
`,
	})

	if !hasRequirement(result, plan.RequirementRuntime, "python", "graalpy-24.1") {
		t.Fatalf("missing graalpy-24.1 from .python-version in %+v", result.Findings)
	}
}

func TestDetectSkipsRemotePipInstalls(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - run: pip install ruff pytest
      - run: python -m pip install coveralls
      - run: pip install -c constraints.txt tox
`,
	})

	if commands := commandByName(result); len(commands) != 0 {
		t.Fatalf("remote pip install was emitted as a repository command: %+v", result.Findings)
	}
}

func TestDetectRetainsRelativeLocalPipInstalls(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - run: pip install ../shared
      - run: pip install packages/widget
`,
	})

	found := map[string]bool{}
	for _, finding := range result.Findings {
		item, ok := finding.(plan.CommandFinding)
		if !ok || item.Command.Run == nil {
			continue
		}
		found[*item.Command.Run] = true
	}
	for _, run := range []string{"pip install ../shared", "pip install packages/widget"} {
		if !found[run] {
			t.Fatalf("missing %q in %+v", run, result.Findings)
		}
	}
}

func TestDetectSkipsRemoteGemInstalls(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - run: gem install test-unit coveralls
`,
	})

	if commands := commandByName(result); len(commands) != 0 {
		t.Fatalf("remote gem install was emitted as a repository command: %+v", result.Findings)
	}
}

func TestDetectSkipsSystemPackageProvisioning(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - run: sudo apt-get update && sudo apt-get install -y libvips
`,
	})

	if commands := commandByName(result); len(commands) != 0 {
		t.Fatalf("system package provisioning was emitted as repository commands: %+v", result.Findings)
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

func TestDetectSkipsEnvSshAndRsyncPlumbing(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - run: |
          env
          ssh host echo hi
          rsync -a src/ dest/
          go test ./...
`,
	})

	if got := keys(commandByName(result)); !slices.Equal(got, []string{"go test"}) {
		t.Fatalf("commands = %v, want only go test", got)
	}
}

func TestDetectSkipsPHPDiagnosticProbes(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - run: |
          php -i
          php --ini
          php -m
          composer -v install
`,
	})

	if got := keys(commandByName(result)); !slices.Equal(got, []string{"install dependencies"}) {
		t.Fatalf("commands = %v, want only composer -v install", got)
	}
}

func TestDetectFansOutMatrixCdBeforeComposerTest(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    strategy:
      matrix:
        dir: [packages/app, packages/lib]
    steps:
      - run: |
          cd ${{ matrix.dir }}
          composer test
          composer phpstan
`,
	})
	for _, name := range []string{"test", "phpstan"} {
		dirs := commandDirectories(result, name)
		if !slices.Equal(sortedCopy(dirs), []string{"packages/app", "packages/lib"}) {
			t.Fatalf("%s directories = %v, want packages/app and packages/lib", name, dirs)
		}
	}
}

func TestDetectNamesUnwrappedPHPUnitWithoutFilterValues(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - run: composer exec phpunit -- --group Elasticsearch
`,
	})
	got := commandByName(result)["phpunit"]
	if got.Name != "phpunit" || deref(got.Run) != "composer exec phpunit -- --group Elasticsearch" {
		t.Fatalf("command = %+v, want name phpunit", got)
	}
	if _, ok := commandByName(result)["phpunit Elasticsearch"]; ok {
		t.Fatalf("commands = %v, did not want a filter value in the name", keys(commandByName(result)))
	}
}

func TestDetectExpandsMatrixComposerWorkingDir(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    strategy:
      matrix:
        dir: [packages/app, packages/lib]
    steps:
      - run: composer --working-dir=${{ matrix.dir }} test
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

func TestDetectLeavesUnresolvedComposerWorkingDirOnTheParent(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - run: composer --working-dir=${{ inputs.dir }} exec phpunit
`,
	})
	dirs := commandDirectories(result, "phpunit")
	if !slices.Equal(dirs, []string{"."}) {
		t.Fatalf("directories = %v, want the parent project", dirs)
	}
}

func TestDetectAppliesComposerWorkingDirAfterExec(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - run: composer exec -d tools phpunit
`,
	})

	got := commandByName(result)["phpunit"]
	if deref(got.Run) != "composer exec -d tools phpunit" || got.Directory != "tools" {
		t.Fatalf("command = %+v, want phpunit in tools", got)
	}
	if !commandHasCapability(got, plan.CapabilityTestRun) {
		t.Fatalf("interpretations = %+v, want test.run", got.Interpretations)
	}
	if _, ok := commandByName(result)["tools phpunit"]; ok {
		t.Fatalf("commands = %v, did not want the working-dir value as the name", keys(commandByName(result)))
	}
}

func TestDetectAppliesComposerWorkingDirWhenUnwrappingExec(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - run: composer -d tools exec phpunit
`,
	})

	got := commandByName(result)["phpunit"]
	if deref(got.Run) != "composer -d tools exec phpunit" || got.Directory != "tools" {
		t.Fatalf("command = %+v, want phpunit in tools", got)
	}
	if !commandHasCapability(got, plan.CapabilityTestRun) {
		t.Fatalf("interpretations = %+v, want test.run", got.Interpretations)
	}
}

func TestDetectKeepsComposerVerboseInstallAndSkipsComposerVersion(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    steps:
      - run: |
          composer -V
          composer --version
          composer -v install
`,
	})

	if got := keys(commandByName(result)); !slices.Equal(got, []string{"install dependencies"}) {
		t.Fatalf("commands = %v, want only composer -v install", got)
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
          docker version
          docker info
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
	versions := runtimeRequirementVersions(result, "node")
	if !slices.Equal(versions, []string{"18"}) {
		t.Fatalf("node versions = %v, want only 18", versions)
	}
}

func TestDetectKeepsGHAExpressionsAtomicInRunText(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  build:
    steps:
      - run: |
          platform=${{ matrix.platform }}
          echo "PLATFORM_PAIR=${platform}" >> $GITHUB_ENV
      - run: platform=${{ matrix.platform }} docker build .
`,
	})
	for _, finding := range result.Findings {
		item, ok := finding.(plan.CommandFinding)
		if !ok {
			continue
		}
		run := deref(item.Command.Run)
		if strings.Contains(item.Command.Name, "matrix.platform") || strings.Contains(run, "matrix.platform }}") {
			t.Fatalf("fabricated command from a split expression: %+v", item.Command)
		}
	}
	commands := commandByName(result)
	if got := deref(commands["docker build"].Run); got != "platform=$platform docker build ." {
		t.Fatalf("docker build = %q, want redacted assignment kept with docker build", got)
	}
}

func TestDetectFansOutMatrixServiceImages(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    strategy:
      matrix:
        postgres_image: ["postgres:18"]
    services:
      postgres:
        image: ${{ matrix.postgres_image }}
    steps:
      - run: echo skip
`,
	})
	if !hasRequirement(result, plan.RequirementService, "postgres", "18") {
		t.Fatalf("missing resolved postgres 18 in %+v", result.Findings)
	}
	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if ok && item.Requirement.Kind == plan.RequirementService && strings.Contains(item.Requirement.Name, "${{") {
			t.Fatalf("service name leaked an expression: %+v", item.Requirement)
		}
	}
}

func TestDetectRecordsUnevaluableServiceImageAsMatrixFact(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".github/workflows/ci.yml": `
jobs:
  test:
    services:
      postgres:
        image: ${{ env.POSTGRES_IMAGE }}
    steps:
      - run: echo skip
`,
	})
	if !slices.Contains(factValues(result, "ci.matrix.postgres"), "${{ env.POSTGRES_IMAGE }}") {
		t.Fatalf("facts = %v, want ci.matrix.postgres for the unresolved image", factValues(result, "ci.matrix.postgres"))
	}
	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if ok && item.Requirement.Kind == plan.RequirementService && strings.Contains(item.Requirement.Name, "${{") {
			t.Fatalf("unevaluable image became a service name: %+v", item.Requirement)
		}
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

func runtimeRequirementVersions(result provider.Result, name string) []string {
	var versions []string
	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if !ok || item.Requirement.Kind != plan.RequirementRuntime || item.Requirement.Name != name {
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
