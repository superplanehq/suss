package makefile

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

func TestDetectReturnsNothingWithoutMakefile(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{"README.md": "hello\n"})
	if len(result.Findings) != 0 {
		t.Fatalf("Detect() = %+v, want no findings", result)
	}
}

func TestDetectEnumeratesTargetsAndInterpretsRecipes(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"Makefile": "" +
			".PHONY: test lint install\n" +
			"\n" +
			"install:\n" +
			"\tgo mod download\n" +
			"\n" +
			"test:\n" +
			"\tgo test ./...\n" +
			"\n" +
			"lint:\n" +
			"\tgolangci-lint run\n",
	})

	if !hasRequirement(result, plan.RequirementTool, "make", "") {
		t.Fatalf("missing make tool in %+v", result.Findings)
	}

	commands := commandByName(result)
	if deref(commands["test"].Run) != "make test" {
		t.Fatalf("test = %+v, want make test", commands["test"])
	}
	if commands["test"].Origin != plan.CommandDeclared {
		t.Fatalf("test origin = %s, want declared", commands["test"].Origin)
	}
	if !hasCapability(commands["test"], plan.CapabilityTestRun) {
		t.Fatalf("test interpretations = %+v, want test.run", commands["test"].Interpretations)
	}
	if !hasCapability(commands["lint"], plan.CapabilityCodeLint) {
		t.Fatalf("lint interpretations = %+v, want code.lint", commands["lint"].Interpretations)
	}
	if !hasCapability(commands["install"], plan.CapabilityDependenciesInstall) {
		t.Fatalf("install interpretations = %+v, want dependencies.install", commands["install"].Interpretations)
	}
	if _, ok := commands[".PHONY"]; ok {
		t.Fatalf("commands = %+v, did not want .PHONY", commands)
	}
}

func TestDetectInterpretsComposerInstallWithGlobalOptions(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"Makefile": "" +
			"install:\n" +
			"\tcomposer --no-interaction install\n" +
			"\n" +
			"ci-install:\n" +
			"\tcomposer -v install\n",
	})

	commands := commandByName(result)
	if !hasCapability(commands["install"], plan.CapabilityDependenciesInstall) {
		t.Fatalf("install interpretations = %+v, want dependencies.install", commands["install"].Interpretations)
	}
	if !hasCapability(commands["ci-install"], plan.CapabilityDependenciesInstall) {
		t.Fatalf("ci-install interpretations = %+v, want dependencies.install", commands["ci-install"].Interpretations)
	}
}

func TestDetectInterpretsComposerInstallAlias(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"Makefile": "" +
			"install:\n" +
			"\tcomposer i\n",
	})

	commands := commandByName(result)
	if !hasCapability(commands["install"], plan.CapabilityDependenciesInstall) {
		t.Fatalf("install interpretations = %+v, want dependencies.install for composer i", commands["install"].Interpretations)
	}
}

func TestDetectExpandsSimpleVariablesAndRecordsLimitations(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"Makefile": "" +
			"GO_TEST_FLAGS ?=\n" +
			"include extra.mk\n" +
			"\n" +
			"ifeq ($(CI),true)\n" +
			"EXTRA = -count=1\n" +
			"endif\n" +
			"\n" +
			"test:\n" +
			"\tgo test -v $(GO_TEST_FLAGS) ./...\n" +
			"\n" +
			"%.o: %.c\n" +
			"\t$(CC) -c $<\n",
	})

	commands := commandByName(result)
	if !hasCapability(commands["test"], plan.CapabilityTestRun) {
		t.Fatalf("test interpretations = %+v, want test.run after expanding GO_TEST_FLAGS", commands["test"].Interpretations)
	}
	if _, ok := commands["%.o"]; ok {
		t.Fatalf("commands = %+v, did not want a pattern rule", commands)
	}

	limits := factValues(result, "provider.make.limitation")
	if !slices.Contains(limits, "include") || !slices.Contains(limits, "conditionals") || !slices.Contains(limits, "variable-expansion") {
		t.Fatalf("limitations = %v, want include, conditionals, and variable-expansion", limits)
	}
}

func TestDetectPrefersGNUmakefileAndReportsDocker(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"makefile": "ignored:\n\techo no\n",
		"GNUmakefile": "" +
			"postgres:\n" +
			"\tdocker run --detach postgres:16\n",
	})

	commands := commandByName(result)
	if _, ok := commands["ignored"]; ok {
		t.Fatalf("commands = %+v, want GNUmakefile to win", commands)
	}
	if deref(commands["postgres"].Run) != "make postgres" {
		t.Fatalf("postgres = %+v, want make postgres", commands["postgres"])
	}
	if !hasRequirement(result, plan.RequirementTool, "docker", "") {
		t.Fatalf("missing docker tool in %+v", result.Findings)
	}
}

func TestDetectPreservesUnresolvedVariablesAsLimitations(t *testing.T) {
	t.Parallel()

	parsed := parseMakefile("build:\n\t$(CC) -o app main.c\n")
	if len(parsed.targets) != 1 || parsed.targets[0].Recipe != "$(CC) -o app main.c" {
		t.Fatalf("recipe = %+v, want $(CC) preserved rather than erased", parsed.targets)
	}
	if !slices.Contains(parsed.limitations, "variable-expansion") {
		t.Fatalf("limitations = %v, want variable-expansion for unresolved $(CC)", parsed.limitations)
	}

	result := detectFiles(t, map[string]string{"Makefile": "build:\n\t$(CC) -o app main.c\n"})
	if !slices.Contains(factValues(result, "provider.make.limitation"), "variable-expansion") {
		t.Fatalf("limitations = %v, want variable-expansion", factValues(result, "provider.make.limitation"))
	}
}

func TestDetectCapturesInlineRecipes(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"Makefile": "test: ; go test ./...\n",
	})
	commands := commandByName(result)
	if !hasCapability(commands["test"], plan.CapabilityTestRun) {
		t.Fatalf("inline test = %+v, want test.run from the inline recipe", commands["test"])
	}
	if commands["test"].Evidence[0].Description == "" {
		t.Fatalf("inline recipe produced no evidence: %+v", commands["test"])
	}
}

func TestDetectIgnoresMakeFunctionStatements(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"Makefile": "" +
			"ifeq (, $(shell which golangci-lint))\n" +
			"$(warning \"could not find golangci-lint in $(PATH), run: curl -sfL https://example.com | sh\")\n" +
			"endif\n" +
			"\n" +
			"lint:\n" +
			"\tgolangci-lint run\n",
	})

	commands := commandByName(result)
	if len(commands) != 1 || commands["lint"].Name != "lint" {
		t.Fatalf("commands = %+v, want only lint", commands)
	}
}

func TestParseMakefileExpandsRecursiveAssignments(t *testing.T) {
	t.Parallel()

	parsed := parseMakefile("" +
		"BIN = ./bin\n" +
		"CMD = $(BIN)/suss\n" +
		"build:\n" +
		"\tgo build -o $(CMD)\n")
	if len(parsed.targets) != 1 || parsed.targets[0].Name != "build" {
		t.Fatalf("targets = %+v, want build", parsed.targets)
	}
	if parsed.targets[0].Recipe != "go build -o ./bin/suss" {
		t.Fatalf("recipe = %q, want expanded go build", parsed.targets[0].Recipe)
	}
}

func TestParseMakefileDoesNotExpandShellAssignments(t *testing.T) {
	t.Parallel()

	parsed := parseMakefile("" +
		"GO != which go\n" +
		"test:\n" +
		"\t$(GO) test ./...\n")
	if len(parsed.targets) != 1 || parsed.targets[0].Name != "test" {
		t.Fatalf("targets = %+v, want test", parsed.targets)
	}
	if parsed.targets[0].Recipe != "$(GO) test ./..." {
		t.Fatalf("recipe = %q, want unresolved $(GO) rather than the shell command", parsed.targets[0].Recipe)
	}
	if !slices.Contains(parsed.limitations, "variable-expansion") {
		t.Fatalf("limitations = %v, want variable-expansion for != assignment", parsed.limitations)
	}
}

func detectFiles(t *testing.T, files map[string]string) provider.Result {
	t.Helper()

	root := t.TempDir()
	for name, contents := range files {
		writeFile(t, filepath.Join(root, name), contents)
	}

	result, err := Provider{}.Detect(provider.Context{RepositoryRoot: root, ProjectPath: "."})
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	return result
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

func hasCapability(command plan.Command, capability plan.Capability) bool {
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

func factValues(result provider.Result, name string) []string {
	var values []string
	for _, finding := range result.Findings {
		item, ok := finding.(plan.PropertyFinding)
		if !ok || item.Property.Name != name {
			continue
		}
		values = append(values, item.Property.Value)
	}
	return values
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func TestRecipeSummaryDoesNotIncludeAssignmentValues(t *testing.T) {
	t.Parallel()

	if strings.Contains(recipeSummary("docker run -e POSTGRES_PASSWORD=secret postgres:16"), "secret") {
		t.Fatal("recipe summary exposed an assignment value")
	}
	if strings.Contains(recipeSummary("$(info running tests)\n\trichgo test"), "$(info") {
		t.Fatal("recipe summary included a Make function invocation")
	}
}
