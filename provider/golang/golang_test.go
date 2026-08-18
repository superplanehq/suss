package golang

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

func TestDetectReturnsNothingWithoutGoMod(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{"README.md": "hello\n"})
	if len(result.Findings) != 0 || len(result.Ambiguities) != 0 || len(result.Conflicts) != 0 {
		t.Fatalf("Detect() = %+v, want an empty result", result)
	}
}

func TestDetectReadsModuleVersionAndInfersConventions(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"go.mod":        "module example.com/lib\n\ngo 1.22\n",
		"lib.go":        "package lib\n",
		"lib_test.go":   "package lib\n",
		".golangci.yml": "version: \"2\"\n",
	})
	project := assembleProject(t, ".", result)

	if got := names(project.Languages); !slices.Equal(got, []string{"go"}) {
		t.Fatalf("languages = %v, want go", got)
	}
	if len(project.Requirements) != 1 || project.Requirements[0].Name != "go" || project.Requirements[0].Version != "1.22" {
		t.Fatalf("requirements = %+v, want go 1.22", project.Requirements)
	}
	if !slices.Equal(factValues(project.Facts, "tool.configured"), []string{"golangci-lint"}) {
		t.Fatalf("facts = %+v, want golangci-lint configured", project.Facts)
	}
	if len(project.Preparation) != 1 || deref(project.Preparation[0].Run) != "go mod download" {
		t.Fatalf("preparation = %+v, want go mod download", project.Preparation)
	}
	if project.Preparation[0].Origin != plan.CommandInferred || project.Preparation[0].Confidence != plan.ConfidenceMedium {
		t.Fatalf("preparation origin/confidence = %s/%s, want inferred/medium", project.Preparation[0].Origin, project.Preparation[0].Confidence)
	}

	commands := commandRuns(project.Commands)
	if commands["test"] != "go test ./..." {
		t.Fatalf("test command = %q, want go test ./...", commands["test"])
	}
	if commands["build"] != "go build ./..." {
		t.Fatalf("build command = %q, want go build ./...", commands["build"])
	}
	if commands["vet"] != "go vet ./..." {
		t.Fatalf("vet command = %q, want go vet ./...", commands["vet"])
	}

	testCmd := commandByName(t, project, "test")
	if testCmd.Origin != plan.CommandInferred {
		t.Fatalf("test origin = %s, want inferred", testCmd.Origin)
	}
	if testCmd.Confidence != plan.ConfidenceHigh {
		t.Fatalf("test confidence = %s, want high when test files exist", testCmd.Confidence)
	}
	if !hasConvention(testCmd.Evidence, "test") {
		t.Fatalf("test evidence = %+v, want a go-ecosystem test convention", testCmd.Evidence)
	}

	caps := commandCapabilities(project.Commands)
	if !slices.Contains(caps["test"], plan.CapabilityTestRun) {
		t.Fatalf("test interpretations = %v, want test.run", caps["test"])
	}
	if !slices.Contains(caps["build"], plan.CapabilityArtifactBuild) {
		t.Fatalf("build interpretations = %v, want artifact.build", caps["build"])
	}
	if !slices.Contains(caps["vet"], plan.CapabilityCodeLint) {
		t.Fatalf("vet interpretations = %v, want code.lint", caps["vet"])
	}
}

func TestDetectOmitsGoTestWhenNoTestFilesExist(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"go.mod": "module example.com/lib\n\ngo 1.22\n",
		"lib.go": "package lib\n",
	})
	project := assembleProject(t, ".", result)

	if _, ok := commandRuns(project.Commands)["test"]; ok {
		t.Fatalf("commands = %+v, did not want an inferred test without test files", project.Commands)
	}
	if commandRuns(project.Commands)["build"] != "go build ./..." {
		t.Fatalf("commands = %+v, want inferred build", project.Commands)
	}
}

func TestDetectReportsGoWorkAsAWorkspaceFact(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"go.mod":  "module example.com/workspace\n\ngo 1.22\n",
		"go.work": "go 1.22\n\nuse .\n",
	})
	project := assembleProject(t, ".", result)

	if !slices.Equal(factValues(project.Facts, "workspace.orchestrator"), []string{"go"}) {
		t.Fatalf("facts = %+v, want workspace.orchestrator=go", project.Facts)
	}
	if len(project.Commands) == 0 || project.Commands[0].Scope != plan.ScopeRepository {
		t.Fatalf("commands = %+v, want repository scope on a workspace root", project.Commands)
	}
}

func TestDetectIgnoresNestedModuleTestFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/root\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "nested", "go.mod"), "module example.com/nested\n\ngo 1.22\n")
	writeFile(t, filepath.Join(root, "nested", "nested_test.go"), "package nested\n")

	result, err := Provider{}.Detect(provider.Context{RepositoryRoot: root, ProjectPath: "."})
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	project := assembleProject(t, ".", result)
	if _, ok := commandRuns(project.Commands)["test"]; ok {
		t.Fatalf("commands = %+v, did not want a test inferred from a nested module", project.Commands)
	}
}

func TestDetectIgnoresTestFilesInTemporaryDependencyCaches(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"go.mod": "module example.com/root\n\ngo 1.22\n",
		"tmp/go/pkg/mod/example.com/module/module_test.go": "package module\n",
	})
	project := assembleProject(t, ".", result)

	if _, ok := commandRuns(project.Commands)["test"]; ok {
		t.Fatalf("commands = %+v, did not want a test inferred from a temporary dependency cache", project.Commands)
	}
}

func TestDetectUsesNestedProjectPathsInEvidence(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "backend", "go.mod"), "module example.com/backend\n\ngo 1.24\n")
	writeFile(t, filepath.Join(root, "backend", "server_test.go"), "package backend\n")

	result, err := Provider{}.Detect(provider.Context{RepositoryRoot: root, ProjectPath: "backend"})
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	project := assembleProject(t, "backend", result)
	testCmd := commandByName(t, project, "test")
	if testCmd.Directory != "backend" {
		t.Fatalf("directory = %q, want backend", testCmd.Directory)
	}
	if testCmd.Evidence[0].Source != "backend/go.mod" {
		t.Fatalf("evidence source = %q, want backend/go.mod", testCmd.Evidence[0].Source)
	}

	id, err := plan.NewCommandID(plan.CommandIdentity{
		ProjectPath: "backend",
		Provider:    "go",
		Source:      "backend/go.mod",
		Pointer:     "/#test",
	})
	if err != nil {
		t.Fatalf("NewCommandID() error = %v", err)
	}
	if testCmd.ID != id {
		t.Fatalf("id = %q, want %q", testCmd.ID, id)
	}
}

func TestParseGoModReadsModuleAndVersion(t *testing.T) {
	t.Parallel()

	got := parseGoMod("module example.com/app\n\n// comment\ngo 1.25.1\n\nrequire (\n\tgithub.com/foo/bar v1.0.0\n)\n")
	if got.Module != "example.com/app" || got.Version != "1.25.1" {
		t.Fatalf("parseGoMod() = %+v, want module example.com/app and go 1.25.1", got)
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

func assembleProject(t *testing.T, path string, result provider.Result) plan.ProjectPlan {
	t.Helper()

	project := plan.NewProjectPlan(path)
	for _, finding := range result.Findings {
		switch item := finding.(type) {
		case plan.PropertyFinding:
			applyProperty(&project, item.Property)
		case plan.RequirementFinding:
			project.Requirements = append(project.Requirements, item.Requirement)
		case plan.CommandFinding:
			if item.Command.Origin == plan.CommandInferred && isPreparation(item.Command) {
				project.Preparation = append(project.Preparation, item.Command)
			} else {
				project.Commands = append(project.Commands, item.Command)
			}
		default:
			t.Fatalf("unexpected finding type %T", finding)
		}
	}
	project.Ambiguities = result.Ambiguities
	project.Conflicts = result.Conflicts
	if hasFact(project.Facts, "workspace.orchestrator") {
		for i := range project.Commands {
			project.Commands[i].Scope = plan.ScopeRepository
		}
		for i := range project.Preparation {
			project.Preparation[i].Scope = plan.ScopeRepository
		}
	}
	document := plan.NewDocument([]plan.ProjectPlan{project})
	document.Sort()
	if err := document.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	return document.Projects[0]
}

func applyProperty(project *plan.ProjectPlan, property plan.Property) {
	switch property.Kind {
	case plan.PropertyLanguage:
		project.Languages = append(project.Languages, plan.DetectedValue{Name: property.Name, Confidence: property.Confidence, Evidence: property.Evidence})
	case plan.PropertyFramework:
		project.Frameworks = append(project.Frameworks, plan.DetectedValue{Name: property.Name, Confidence: property.Confidence, Evidence: property.Evidence})
	case plan.PropertyPackageManager:
		project.PackageManagers = append(project.PackageManagers, plan.DetectedTool{Name: property.Name, Version: property.Version, Confidence: property.Confidence, Evidence: property.Evidence})
	case plan.PropertyFact:
		project.Facts = append(project.Facts, plan.ProjectFact{Name: property.Name, Value: property.Value, Confidence: property.Confidence, Evidence: property.Evidence})
	}
}

func isPreparation(command plan.Command) bool {
	for _, interpretation := range command.Interpretations {
		if interpretation.Capability == plan.CapabilityDependenciesInstall {
			return true
		}
	}
	return false
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

func names(values []plan.DetectedValue) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, value.Name)
	}
	return out
}

func commandRuns(commands []plan.Command) map[string]string {
	out := make(map[string]string, len(commands))
	for _, command := range commands {
		out[command.Name] = deref(command.Run)
	}
	return out
}

func commandCapabilities(commands []plan.Command) map[string][]plan.Capability {
	out := make(map[string][]plan.Capability, len(commands))
	for _, command := range commands {
		caps := make([]plan.Capability, 0, len(command.Interpretations))
		for _, interpretation := range command.Interpretations {
			caps = append(caps, interpretation.Capability)
		}
		out[command.Name] = caps
	}
	return out
}

func commandByName(t *testing.T, project plan.ProjectPlan, name string) plan.Command {
	t.Helper()
	for _, command := range project.Commands {
		if command.Name == name {
			return command
		}
	}
	t.Fatalf("command %q not found in %+v", name, project.Commands)
	return plan.Command{}
}

func factValues(facts []plan.ProjectFact, name string) []string {
	var values []string
	for _, fact := range facts {
		if fact.Name == name {
			values = append(values, fact.Value)
		}
	}
	return values
}

func hasFact(facts []plan.ProjectFact, name string) bool {
	for _, fact := range facts {
		if fact.Name == name {
			return true
		}
	}
	return false
}

func hasConvention(evidence []plan.Evidence, pointer string) bool {
	for _, item := range evidence {
		if item.Kind == plan.EvidenceConvention && item.Source == "go-ecosystem" && item.Pointer == pointer {
			return true
		}
	}
	return false
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
