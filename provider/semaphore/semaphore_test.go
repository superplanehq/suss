package semaphore

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

func TestDetectReturnsNothingWithoutPipelines(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{"README.md": "hello\n"})
	if len(result.Findings) != 0 {
		t.Fatalf("Detect() = %+v, want no findings", result)
	}
}

func TestDetectReadsPipelineCommandsVersionsServicesAndDirectories(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"app/.keep": "",
		"web/.keep": "",
		".semaphore/semaphore.yml": `version: v1.0
name: CI
global_job_config:
  prologue:
    commands:
      - checkout
      - sem-version elixir 1.18.1
      - sem-version erlang 27.2
      - sem-service start postgres 16
      - cd app
      - mix deps.get
blocks:
  - name: Tests
    task:
      jobs:
        - name: Unit
          commands:
            - mix test --warnings-as-errors
            - API_TOKEN=hunter2 mix custom
        - name: Frontend
          commands:
            - cd ../web
            - npm test
      epilogue:
        always:
          commands:
            - test-results publish junit.xml
after_pipeline:
  task:
    jobs:
      - name: Report
        commands:
          - test-results gen-pipeline-report
`,
	})

	commands := commandRuns(result)
	if !slices.Contains(commands["app"], "mix deps.get") || !slices.Contains(commands["app"], "mix test --warnings-as-errors") {
		t.Fatalf("app commands = %v, want Mix install and test", commands["app"])
	}
	if !slices.Contains(commands["web"], "npm test") {
		t.Fatalf("web commands = %v, want npm test", commands["web"])
	}
	if !slices.Contains(commands["app"], "API_TOKEN=$API_TOKEN mix custom") {
		t.Fatalf("app commands = %v, want redacted assignment", commands["app"])
	}
	if hasCommandExecutable(result, "checkout") || hasCommandExecutable(result, "test-results") ||
		hasCommandExecutable(result, "sem-version") || hasCommandExecutable(result, "sem-service") {
		t.Fatalf("Semaphore plumbing leaked into commands: %+v", result.Findings)
	}
	if !hasRequirement(result, plan.RequirementRuntime, "elixir", "1.18.1") ||
		!hasRequirement(result, plan.RequirementRuntime, "erlang", "27.2") ||
		!hasRequirement(result, plan.RequirementService, "postgres", "16") {
		t.Fatalf("missing runtime/service findings in %+v", result.Findings)
	}
}

func TestDetectAppliesCargoDirectoryFlags(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"crates/tool/Cargo.toml": "[package]\nname = \"tool\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
		".semaphore/semaphore.yml": `version: v1.0
name: CI
blocks:
  - name: Tests
    task:
      jobs:
        - name: Unit
          commands:
            - cargo test --manifest-path crates/tool/Cargo.toml
            - cargo -C crates/tool build
`,
	})

	commands := commandRuns(result)
	if !slices.Contains(commands["."], "cargo test --manifest-path crates/tool/Cargo.toml") {
		t.Fatalf("commands = %v, want the original manifest-path command on the workspace root", commands)
	}
	if !slices.Contains(commands["crates/tool"], "cargo build") {
		t.Fatalf("commands = %v, want cargo build attached to crates/tool without -C", commands)
	}
	if slices.Contains(commands["crates/tool"], "cargo test") {
		t.Fatalf("commands = %v, did not want a rewritten manifest-path command on crates/tool", commands)
	}
}

func TestDetectComposesCargoDirectoryAndManifestPath(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"crates/tool/Cargo.toml": "[package]\nname = \"tool\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
		".semaphore/semaphore.yml": `version: v1.0
name: CI
blocks:
  - name: Tests
    task:
      jobs:
        - name: Unit
          commands:
            - cargo -C crates/tool test --manifest-path Cargo.toml
`,
	})

	commands := commandRuns(result)
	if !slices.Contains(commands["crates/tool"], "cargo test --manifest-path Cargo.toml") {
		t.Fatalf("commands = %v, want cargo test on crates/tool with the manifest path kept", commands)
	}
	if slices.Contains(commands["."], "cargo test") || slices.Contains(commands["."], "cargo -C crates/tool test --manifest-path Cargo.toml") {
		t.Fatalf("commands = %v, did not want the composed command on the workspace root", commands)
	}
}

func TestDetectRewritesCargoDirectoryFlagsThroughRustup(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"crates/tool/Cargo.toml": "[package]\nname = \"tool\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
		".semaphore/semaphore.yml": `version: v1.0
name: CI
blocks:
  - name: Tests
    task:
      jobs:
        - name: Unit
          commands:
            - rustup run nightly cargo test --manifest-path crates/tool/Cargo.toml
`,
	})

	commands := commandRuns(result)
	if !slices.Contains(commands["."], "rustup run nightly cargo test --manifest-path crates/tool/Cargo.toml") {
		t.Fatalf("commands = %v, want the original rustup manifest-path command on the workspace root", commands)
	}
}

func TestDetectPreservesCargoCWithShellRedirects(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"crate/Cargo.toml": "[package]\nname = \"crate\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
		".semaphore/semaphore.yml": `version: v1.0
name: CI
blocks:
  - name: Tests
    task:
      jobs:
        - name: Unit
          commands:
            - cargo -C crate test > result.log
`,
	})

	commands := commandRuns(result)
	if !slices.Contains(commands["."], "cargo -C crate test > result.log") {
		t.Fatalf("commands = %v, want the original -C redirect command on the repository root", commands)
	}
	if slices.Contains(commands["crate"], "cargo test > result.log") || slices.Contains(commands["crate"], "cargo test") {
		t.Fatalf("commands = %v, did not want a rewritten redirect on crate/", commands)
	}
}

func TestDetectPreservesCargoFlagsWhenManifestDirectoryIsMissing(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".semaphore/semaphore.yml": `version: v1.0
name: CI
blocks:
  - name: Tests
    task:
      jobs:
        - name: Unit
          commands:
            - cargo test --manifest-path generated/tool/Cargo.toml
`,
	})

	commands := commandRuns(result)
	if !slices.Contains(commands["."], "cargo test --manifest-path generated/tool/Cargo.toml") {
		t.Fatalf("commands = %v, want the original manifest-path command when the target directory is absent", commands)
	}
	if slices.Contains(commands["."], "cargo test") {
		t.Fatalf("commands = %v, did not want cargo test rewritten onto the repository root", commands)
	}
}

func TestDetectLeavesDynamicCargoManifestPathUnresolved(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"crates/tool/Cargo.toml": "[package]\nname = \"tool\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
		".semaphore/semaphore.yml": `version: v1.0
name: CI
blocks:
  - name: Tests
    task:
      jobs:
        - name: Unit
          commands:
            - cargo test --manifest-path $SEMAPHORE_GIT_DIR/crates/tool/Cargo.toml
`,
	})

	commands := commandRuns(result)
	if !slices.Contains(commands["."], "cargo test --manifest-path $SEMAPHORE_GIT_DIR/crates/tool/Cargo.toml") {
		t.Fatalf("commands = %v, want the original dynamic manifest-path command on the workspace root", commands)
	}
	if slices.Contains(commands["crates/tool"], "cargo test") {
		t.Fatalf("commands = %v, did not want a rewritten command on crates/tool", commands)
	}
}

func TestDetectAppliesUvDirectoryToCommandDirectory(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"packages/api/.keep": "",
		"packages/web/.keep": "",
		".semaphore/semaphore.yml": `version: v1.0
name: CI
blocks:
  - name: Tests
    task:
      jobs:
        - name: Unit
          commands:
            - uv run --directory packages/api pytest
            - uv --directory packages/web run pytest
`,
	})

	commands := commandRuns(result)
	if !slices.Contains(commands["packages/api"], "uv run --directory packages/api pytest") {
		t.Fatalf("commands = %v, want uv run under packages/api", commands)
	}
	if !slices.Contains(commands["packages/web"], "uv --directory packages/web run pytest") {
		t.Fatalf("commands = %v, want uv global --directory under packages/web", commands)
	}
}

func TestDetectLeavesComplexShellProgramsUninterpreted(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".semaphore/semaphore.yml": `version: v1.0
name: Release
blocks:
  - name: Tag
    task:
      jobs:
        - name: Tag
          commands:
            - if [[ "$SEMAPHORE_GIT_REF_TYPE" = "branch" ]]; then make release; fi
            - |
              github_api() {
                curl "$@"
              }
              if [ -n "$VERSION" ]; then
                github_api https://example.test
              fi
`,
	})

	if len(commandRuns(result)) != 0 {
		t.Fatalf("complex shell emitted commands: %+v", result.Findings)
	}
}

func TestDetectUsesOnlyPipelineWideEnvironmentDefaults(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".semaphore/semaphore.yml": `version: v1.0
name: CI
global_job_config:
  env_vars:
    - name: MIX_ENV
      value: test
blocks:
  - name: Tests
    task:
      env_vars:
        - name: BLOCK_WIRING
          value: internal
      jobs:
        - name: Unit
          env_vars:
            - name: JOB_WIRING
              value: internal
          commands:
            - mix test
`,
	})

	if !hasRequirement(result, plan.RequirementEnvironment, "MIX_ENV", "") {
		t.Fatalf("missing pipeline environment finding in %+v", result.Findings)
	}
	if hasRequirement(result, plan.RequirementEnvironment, "BLOCK_WIRING", "") ||
		hasRequirement(result, plan.RequirementEnvironment, "JOB_WIRING", "") {
		t.Fatalf("task or job wiring leaked into requirements: %+v", result.Findings)
	}
}

func TestDetectAfterPipelineHasIndependentPrologue(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"app/.keep":     "",
		"reports/.keep": "",
		".semaphore/semaphore.yml": `version: v1.0
name: CI
global_job_config:
  prologue:
    commands:
      - cd app
blocks:
  - name: Tests
    task:
      jobs:
        - name: Unit
          commands: [mix test]
after_pipeline:
  task:
    prologue:
      commands:
        - cd reports
    jobs:
      - name: Report
        commands: [mix report]
`,
	})

	commands := commandRuns(result)
	if !slices.Contains(commands["app"], "mix test") {
		t.Fatalf("app commands = %v, want mix test", commands["app"])
	}
	if !slices.Contains(commands["reports"], "mix report") {
		t.Fatalf("report commands = %v, want independent after-pipeline directory", commands["reports"])
	}
}

func TestDetectExpandsMatrixComposerWorkingDir(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"app/.keep": "",
		"lib/.keep": "",
		".semaphore/semaphore.yml": `version: v1.0
name: Matrix
blocks:
  - name: Tests
    task:
      jobs:
        - name: Packages
          commands:
            - composer --working-dir=$PROJECT test
          matrix:
            - env_var: PROJECT
              values: [app, lib]
`,
	})

	runs := commandRuns(result)
	if !slices.Contains(runs["app"], "composer --working-dir=$PROJECT test") || !slices.Contains(runs["lib"], "composer --working-dir=$PROJECT test") {
		t.Fatalf("commands = %v, want composer test in app and lib", runs)
	}
	if slices.Contains(runs["."], "composer --working-dir=$PROJECT test") {
		t.Fatalf("commands = %v, did not want the matrix command on the root", runs)
	}
}

func TestDetectExpandsJobMatrixForVersionsAndServices(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".semaphore/semaphore.yaml": `version: v1.0
name: Matrix
blocks:
  - name: Tests
    task:
      jobs:
        - name: Matrix job
          commands:
            - sem-version elixir $ELIXIR_VERSION
            - sem-service start $DATABASE
            - mix test
          matrix:
            - env_var: ELIXIR_VERSION
              values: [1.17, 1.18]
            - env_var: DATABASE
              values: [postgres, mysql]
`,
	})

	versions := requirementVersions(result, plan.RequirementRuntime, "elixir")
	if !slices.Equal(versions, []string{"1.17", "1.18"}) {
		t.Fatalf("Elixir versions = %v, want 1.17 and 1.18", versions)
	}
	if !hasRequirement(result, plan.RequirementService, "postgres", "") || !hasRequirement(result, plan.RequirementService, "mysql", "") {
		t.Fatalf("missing matrix services in %+v", result.Findings)
	}
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

func commandRuns(result provider.Result) map[string][]string {
	out := make(map[string][]string)
	for _, finding := range result.Findings {
		item, ok := finding.(plan.CommandFinding)
		if ok && item.Command.Run != nil {
			out[item.Command.Directory] = append(out[item.Command.Directory], *item.Command.Run)
		}
	}
	return out
}

func hasCommandExecutable(result provider.Result, executable string) bool {
	for _, runs := range commandRuns(result) {
		for _, run := range runs {
			if run == executable || len(run) > len(executable) && run[:len(executable)+1] == executable+" " {
				return true
			}
		}
	}
	return false
}

func hasRequirement(result provider.Result, kind plan.RequirementKind, name, version string) bool {
	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if ok && item.Requirement.Kind == kind && item.Requirement.Name == name && item.Requirement.Version == version {
			return true
		}
	}
	return false
}

func requirementVersions(result provider.Result, kind plan.RequirementKind, name string) []string {
	var versions []string
	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if ok && item.Requirement.Kind == kind && item.Requirement.Name == name {
			versions = append(versions, item.Requirement.Version)
		}
	}
	slices.Sort(versions)
	return versions
}
