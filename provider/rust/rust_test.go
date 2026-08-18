package rust

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

func TestDetectReturnsNothingWithoutCargoToml(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{"README.md": "hello\n"})
	if len(result.Findings) != 0 || len(result.Ambiguities) != 0 || len(result.Conflicts) != 0 {
		t.Fatalf("Detect() = %+v, want an empty result", result)
	}
}

func TestDetectReadsManifestAndInfersConventions(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"Cargo.toml": "" +
			"[package]\n" +
			"name = \"lib\"\n" +
			"version = \"0.1.0\"\n" +
			"edition = \"2021\"\n" +
			"rust-version = \"1.81\"\n" +
			"\n" +
			"[dependencies]\n" +
			"axum = \"0.7\"\n" +
			"serde = { version = \"1\", features = [\"derive\"] }\n",
		"Cargo.lock":   "# generated\n",
		"src/lib.rs":   "#[cfg(test)]\nmod tests {\n    #[test]\n    fn ok() {}\n}\n",
		"rustfmt.toml": "edition = \"2021\"\n",
		"clippy.toml":  "avoid-breaking-exported-api = false\n",
		"deny.toml":    "[advisories]\n",
	})
	project := assembleProject(t, ".", result)

	if got := names(project.Languages); !slices.Equal(got, []string{"rust"}) {
		t.Fatalf("languages = %v, want rust", got)
	}
	if got := toolNames(project.PackageManagers); !slices.Equal(got, []string{"cargo"}) {
		t.Fatalf("package managers = %v, want cargo", got)
	}
	if got := names(project.Frameworks); !slices.Equal(got, []string{"axum"}) {
		t.Fatalf("frameworks = %v, want axum", got)
	}
	if len(project.Requirements) != 1 || project.Requirements[0].Name != "rust" || project.Requirements[0].Version != ">=1.81" {
		t.Fatalf("requirements = %+v, want rust >=1.81", project.Requirements)
	}
	if !slices.Equal(factValues(project.Facts, "tool.configured"), []string{"cargo-deny", "clippy", "rustfmt"}) {
		t.Fatalf("facts = %+v, want configured rust tools", project.Facts)
	}
	if len(project.Preparation) != 1 || deref(project.Preparation[0].Run) != "cargo fetch" {
		t.Fatalf("preparation = %+v, want cargo fetch", project.Preparation)
	}
	if project.Preparation[0].Origin != plan.CommandInferred || project.Preparation[0].Confidence != plan.ConfidenceMedium {
		t.Fatalf("preparation origin/confidence = %s/%s, want inferred/medium", project.Preparation[0].Origin, project.Preparation[0].Confidence)
	}

	commands := commandRuns(project.Commands)
	if commands["test"] != "cargo test" {
		t.Fatalf("test command = %q, want cargo test", commands["test"])
	}
	if commands["build"] != "cargo build" {
		t.Fatalf("build command = %q, want cargo build", commands["build"])
	}

	testCmd := commandByName(t, project, "test")
	if testCmd.Origin != plan.CommandInferred {
		t.Fatalf("test origin = %s, want inferred", testCmd.Origin)
	}
	if testCmd.Confidence != plan.ConfidenceHigh {
		t.Fatalf("test confidence = %s, want high when tests exist", testCmd.Confidence)
	}
	if !hasConvention(testCmd.Evidence, "test") {
		t.Fatalf("test evidence = %+v, want a rust-ecosystem test convention", testCmd.Evidence)
	}

	caps := commandCapabilities(project.Commands)
	if !slices.Contains(caps["test"], plan.CapabilityTestRun) {
		t.Fatalf("test interpretations = %v, want test.run", caps["test"])
	}
	if !slices.Contains(caps["build"], plan.CapabilityArtifactBuild) {
		t.Fatalf("build interpretations = %v, want artifact.build", caps["build"])
	}
}

func TestDetectOmitsCargoTestWhenNoTestsExist(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"Cargo.toml": "[package]\nname = \"lib\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
		"src/lib.rs": "pub fn add(a: i32, b: i32) -> i32 { a + b }\n",
	})
	project := assembleProject(t, ".", result)

	if _, ok := commandRuns(project.Commands)["test"]; ok {
		t.Fatalf("commands = %+v, did not want an inferred test without tests", project.Commands)
	}
	if commandRuns(project.Commands)["build"] != "cargo build" {
		t.Fatalf("commands = %+v, want inferred build", project.Commands)
	}
}

func TestDetectFindsIntegrationAndAsyncTests(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"Cargo.toml":     "[package]\nname = \"lib\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
		"src/lib.rs":     "pub fn add(a: i32, b: i32) -> i32 { a + b }\n",
		"tests/smoke.rs": "#[tokio::test]\nasync fn boots() {}\n",
	})
	project := assembleProject(t, ".", result)
	if commandRuns(project.Commands)["test"] != "cargo test" {
		t.Fatalf("commands = %+v, want cargo test from tests/", project.Commands)
	}
}

func TestDetectReportsWorkspaceFactAndInheritsRustVersion(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Cargo.toml"), ""+
		"[workspace]\n"+
		"members = [\"crates/tool\"]\n"+
		"\n"+
		"[workspace.package]\n"+
		"rust-version = \"1.80\"\n")
	writeFile(t, filepath.Join(root, "crates", "tool", "Cargo.toml"), ""+
		"[package]\n"+
		"name = \"tool\"\n"+
		"version = \"0.1.0\"\n"+
		"edition = \"2021\"\n"+
		"rust-version.workspace = true\n")
	writeFile(t, filepath.Join(root, "crates", "tool", "src", "lib.rs"), "#[test]\nfn ok() {}\n")

	rootResult, err := Provider{}.Detect(provider.Context{RepositoryRoot: root, ProjectPath: "."})
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	rootProject := assembleProject(t, ".", rootResult)
	if !slices.Equal(factValues(rootProject.Facts, "workspace.orchestrator"), []string{"cargo"}) {
		t.Fatalf("facts = %+v, want workspace.orchestrator=cargo", rootProject.Facts)
	}
	if len(rootProject.Commands) == 0 || rootProject.Commands[0].Scope != plan.ScopeRepository {
		t.Fatalf("commands = %+v, want repository scope on a workspace root", rootProject.Commands)
	}
	if _, ok := commandRuns(rootProject.Commands)["test"]; ok {
		t.Fatalf("workspace root inferred cargo test from a member crate: %+v", rootProject.Commands)
	}
	if len(rootProject.Requirements) != 1 || rootProject.Requirements[0].Version != ">=1.80" {
		t.Fatalf("workspace requirements = %+v, want rust >=1.80", rootProject.Requirements)
	}

	memberResult, err := Provider{}.Detect(provider.Context{RepositoryRoot: root, ProjectPath: "crates/tool"})
	if err != nil {
		t.Fatalf("member Detect() error = %v", err)
	}
	member := assembleProject(t, "crates/tool", memberResult)
	if len(member.Requirements) != 1 || member.Requirements[0].Version != ">=1.80" {
		t.Fatalf("member requirements = %+v, want inherited rust >=1.80", member.Requirements)
	}
	if commandRuns(member.Commands)["test"] != "cargo test" {
		t.Fatalf("member commands = %+v, want cargo test", member.Commands)
	}
}

func TestDetectReadsToolchainAndToolVersions(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"Cargo.toml":          "[package]\nname = \"lib\"\nversion = \"0.1.0\"\nedition = \"2021\"\nrust-version = \"1.81\"\n",
		"src/lib.rs":          "pub fn ok() {}\n",
		"rust-toolchain.toml": "[toolchain]\nchannel = \"1.81.0\"\n",
		".tool-versions":      "rust 1.81.0\n",
	})
	project := assembleProject(t, ".", result)
	versions := rustRequirementVersions(project)
	if !slices.Equal(versions, []string{"1.81.0", ">=1.81"}) {
		t.Fatalf("rust versions = %v, want both the MSRV range and the toolchain pin", versions)
	}
}

func TestDetectIgnoresNestedCrateTestFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Cargo.toml"), "[package]\nname = \"root\"\nversion = \"0.1.0\"\nedition = \"2021\"\n")
	writeFile(t, filepath.Join(root, "src", "lib.rs"), "pub fn ok() {}\n")
	writeFile(t, filepath.Join(root, "nested", "Cargo.toml"), "[package]\nname = \"nested\"\nversion = \"0.1.0\"\nedition = \"2021\"\n")
	writeFile(t, filepath.Join(root, "nested", "src", "lib.rs"), "#[test]\nfn ok() {}\n")

	result, err := Provider{}.Detect(provider.Context{RepositoryRoot: root, ProjectPath: "."})
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	project := assembleProject(t, ".", result)
	if _, ok := commandRuns(project.Commands)["test"]; ok {
		t.Fatalf("commands = %+v, did not want a test inferred from a nested crate", project.Commands)
	}
}

func TestDetectUsesNestedProjectPathsInEvidence(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "backend", "Cargo.toml"), "[package]\nname = \"backend\"\nversion = \"0.1.0\"\nedition = \"2021\"\n")
	writeFile(t, filepath.Join(root, "backend", "src", "lib.rs"), "#[test]\nfn ok() {}\n")

	result, err := Provider{}.Detect(provider.Context{RepositoryRoot: root, ProjectPath: "backend"})
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	project := assembleProject(t, "backend", result)
	testCmd := commandByName(t, project, "test")
	if testCmd.Directory != "backend" {
		t.Fatalf("directory = %q, want backend", testCmd.Directory)
	}
	if testCmd.Evidence[0].Source != "backend/Cargo.toml" {
		t.Fatalf("evidence source = %q, want backend/Cargo.toml", testCmd.Evidence[0].Source)
	}

	id, err := plan.NewCommandID(plan.CommandIdentity{
		ProjectPath: "backend",
		Provider:    "rust",
		Source:      "backend/Cargo.toml",
		Pointer:     "/#test",
	})
	if err != nil {
		t.Fatalf("NewCommandID() error = %v", err)
	}
	if testCmd.ID != id {
		t.Fatalf("id = %q, want %q", testCmd.ID, id)
	}
}

func TestParseCargoTOMLReadsPackageWorkspaceAndDependencies(t *testing.T) {
	t.Parallel()

	got := parseCargoTOML("" +
		"[package]\n" +
		"name = \"app\" # comment\n" +
		"rust-version = \"1.81\"\n" +
		"\n" +
		"[workspace]\n" +
		"members = [\"crates/tool\"]\n" +
		"\n" +
		"[dependencies]\n" +
		"axum = \"0.7\"\n" +
		"\"actix-web\" = { version = \"4\" }\n")
	if got.Name != "app" || got.RustVersion != "1.81" || !got.HasWorkspace {
		t.Fatalf("parseCargoTOML() = %+v, want app 1.81 workspace", got)
	}
	if !slices.Equal(dependencyNames(got.Dependencies), []string{"axum", "actix-web"}) {
		t.Fatalf("dependencies = %v, want axum and actix-web", got.Dependencies)
	}
}

func TestParseCargoTOMLResolvesRenamedDependencies(t *testing.T) {
	t.Parallel()

	got := parseCargoTOML("" +
		"[dependencies]\n" +
		"web = { package = \"axum\", version = \"0.7\" }\n" +
		"axum = { package = \"tracing\", version = \"0.1\" }\n" +
		"\n" +
		"[dependencies.api]\n" +
		"package = \"actix-web\"\n" +
		"version = \"4\"\n")
	if !slices.Equal(dependencyNames(got.Dependencies), []string{"axum", "tracing", "actix-web"}) {
		t.Fatalf("dependencies = %v, want crate names axum, tracing, actix-web", dependencyNames(got.Dependencies))
	}
	if !slices.Equal(dependencyKeys(got.Dependencies), []string{"web", "axum", "api"}) {
		t.Fatalf("dependency keys = %v, want web, axum, api", dependencyKeys(got.Dependencies))
	}
}

func TestDetectResolvesRenamedFrameworkDependencies(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"Cargo.toml": "" +
			"[package]\n" +
			"name = \"app\"\n" +
			"version = \"0.1.0\"\n" +
			"edition = \"2021\"\n" +
			"\n" +
			"[dependencies]\n" +
			"web = { package = \"axum\", version = \"0.7\" }\n" +
			"axum = { package = \"tracing\", version = \"0.1\" }\n",
		"src/lib.rs": "pub fn ok() {}\n",
	})
	project := assembleProject(t, ".", result)
	if got := names(project.Frameworks); !slices.Equal(got, []string{"axum"}) {
		t.Fatalf("frameworks = %v, want axum from the renamed crate", got)
	}
	if len(project.Frameworks[0].Evidence) == 0 || project.Frameworks[0].Evidence[0].Pointer != "/dependencies/web" {
		t.Fatalf("framework evidence = %+v, want /dependencies/web", project.Frameworks[0].Evidence)
	}
}

func TestDetectDoesNotTreatWorkspaceDependenciesAsFrameworks(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"Cargo.toml": "" +
			"[workspace]\n" +
			"members = [\"crates/tool\"]\n" +
			"\n" +
			"[workspace.dependencies]\n" +
			"axum = \"0.7\"\n",
	})
	project := assembleProject(t, ".", result)
	if len(project.Frameworks) != 0 {
		t.Fatalf("frameworks = %+v, did not want axum from workspace.dependencies", project.Frameworks)
	}
}

func TestDetectPrefersNearestToolchainFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Cargo.toml"), "[workspace]\nmembers = [\"nested\"]\n")
	writeFile(t, filepath.Join(root, "rust-toolchain.toml"), "[toolchain]\nchannel = \"1.80.0\"\n")
	writeFile(t, filepath.Join(root, "nested", "Cargo.toml"), "[package]\nname = \"nested\"\nversion = \"0.1.0\"\nedition = \"2021\"\n")
	writeFile(t, filepath.Join(root, "nested", "rust-toolchain"), "1.81.0\n")
	writeFile(t, filepath.Join(root, "nested", "src", "lib.rs"), "pub fn ok() {}\n")

	result, err := Provider{}.Detect(provider.Context{RepositoryRoot: root, ProjectPath: "nested"})
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	project := assembleProject(t, "nested", result)
	if len(project.Requirements) != 1 || project.Requirements[0].Version != "1.81.0" {
		t.Fatalf("requirements = %+v, want nearest rust-toolchain 1.81.0", project.Requirements)
	}
}

func TestDetectConflictsToolchainOlderThanMSRV(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"Cargo.toml":          "[package]\nname = \"lib\"\nversion = \"0.1.0\"\nedition = \"2021\"\nrust-version = \"1.81\"\n",
		"src/lib.rs":          "pub fn ok() {}\n",
		"rust-toolchain.toml": "[toolchain]\nchannel = \"1.80.0\"\n",
	})
	project := assembleProject(t, ".", result)

	if len(project.Conflicts) != 1 || project.Conflicts[0].Subject != "runtime.rust.version" {
		t.Fatalf("conflicts = %+v, want runtime.rust.version for toolchain below MSRV", project.Conflicts)
	}
	if !strings.Contains(project.Conflicts[0].Message, "rust-version") {
		t.Fatalf("conflict message = %q, want rust-version incompatibility", project.Conflicts[0].Message)
	}
	versions := rustRequirementVersions(project)
	if !slices.Equal(versions, []string{"1.80.0", ">=1.81"}) {
		t.Fatalf("rust versions = %v, want pinned 1.80.0 and MSRV >=1.81", versions)
	}
	for _, requirement := range project.Requirements {
		if requirement.Version == "1.80.0" && requirement.Confidence != plan.ConfidenceMedium {
			t.Fatalf("incompatible pin confidence = %s, want medium", requirement.Confidence)
		}
		if requirement.Version == ">=1.81" && requirement.Confidence != plan.ConfidenceHigh {
			t.Fatalf("MSRV confidence = %s, want high", requirement.Confidence)
		}
	}
}

func TestDetectDoesNotConflictCompatibleToolchainAndMSRV(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"Cargo.toml":          "[package]\nname = \"lib\"\nversion = \"0.1.0\"\nedition = \"2021\"\nrust-version = \"1.81\"\n",
		"src/lib.rs":          "pub fn ok() {}\n",
		"rust-toolchain.toml": "[toolchain]\nchannel = \"1.82.0\"\n",
	})
	project := assembleProject(t, ".", result)
	if len(project.Conflicts) != 0 {
		t.Fatalf("conflicts = %+v, did not want a conflict when the pin satisfies the MSRV", project.Conflicts)
	}
	if !slices.Equal(rustRequirementVersions(project), []string{"1.82.0", ">=1.81"}) {
		t.Fatalf("rust versions = %v, want pin plus MSRV", rustRequirementVersions(project))
	}
}

func TestDetectDoesNotConflictUnevaluableToolchainChannel(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"Cargo.toml":          "[package]\nname = \"lib\"\nversion = \"0.1.0\"\nedition = \"2021\"\nrust-version = \"1.81\"\n",
		"src/lib.rs":          "pub fn ok() {}\n",
		"rust-toolchain.toml": "[toolchain]\nchannel = \"stable\"\n",
	})
	project := assembleProject(t, ".", result)
	if len(project.Conflicts) != 0 {
		t.Fatalf("conflicts = %+v, did not want a guessed conflict for channel stable", project.Conflicts)
	}
}

func TestDetectConflictsDisagreeingExactPins(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"Cargo.toml":          "[package]\nname = \"lib\"\nversion = \"0.1.0\"\nedition = \"2021\"\nrust-version = \"1.74\"\n",
		"src/lib.rs":          "pub fn ok() {}\n",
		"rust-toolchain.toml": "[toolchain]\nchannel = \"1.81.0\"\n",
		".tool-versions":      "rust 1.80.0\n",
	})
	project := assembleProject(t, ".", result)

	if len(project.Conflicts) != 1 || project.Conflicts[0].Subject != "runtime.rust.version" {
		t.Fatalf("conflicts = %+v, want runtime.rust.version", project.Conflicts)
	}
	versions := rustRequirementVersions(project)
	if !slices.Equal(versions, []string{"1.80.0", "1.81.0", ">=1.74"}) {
		t.Fatalf("rust versions = %v, want MSRV range plus both exact pins", versions)
	}
	for _, requirement := range project.Requirements {
		if requirement.Version == ">=1.74" && requirement.Confidence != plan.ConfidenceHigh {
			t.Fatalf("MSRV confidence = %s, want high", requirement.Confidence)
		}
		if (requirement.Version == "1.80.0" || requirement.Version == "1.81.0") && requirement.Confidence != plan.ConfidenceMedium {
			t.Fatalf("exact pin %s confidence = %s, want medium", requirement.Version, requirement.Confidence)
		}
	}
}

func TestDetectInfersTestsFromParameterizedAttributes(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"Cargo.toml": "[package]\nname = \"lib\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
		"src/lib.rs": "#[tokio::test(flavor = \"multi_thread\")]\nasync fn boots() {}\n",
	})
	project := assembleProject(t, ".", result)
	if commandRuns(project.Commands)["test"] != "cargo test" {
		t.Fatalf("commands = %+v, want cargo test from parameterized #[tokio::test]", project.Commands)
	}
}

func TestRustSourceHasTestRecognizesSupportedAttributes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		src  string
		want bool
	}{
		{src: "#[test]\nfn ok() {}", want: true},
		{src: "#[tokio::test]\nasync fn ok() {}", want: true},
		{src: "#[tokio::test(flavor = \"multi_thread\")]\nasync fn ok() {}", want: true},
		{src: "#[rstest]\nfn ok() {}", want: true},
		{src: "pub fn ok() {}", want: false},
	}
	for _, tt := range tests {
		if got := rustSourceHasTest(tt.src); got != tt.want {
			t.Fatalf("rustSourceHasTest(%q) = %v, want %v", tt.src, got, tt.want)
		}
	}
}

func TestParseToolchainFile(t *testing.T) {
	t.Parallel()

	if got := ParseToolchainFile("[toolchain]\nchannel = \"1.81.0\"\n"); got != "1.81.0" {
		t.Fatalf("ParseToolchainFile(toml) = %q, want 1.81.0", got)
	}
	if got := ParseToolchainFile("1.70.0\n"); got != "1.70.0" {
		t.Fatalf("ParseToolchainFile(plain) = %q, want 1.70.0", got)
	}
	if got := ParseToolchainFile("# comment\nstable\n"); got != "stable" {
		t.Fatalf("ParseToolchainFile(channel) = %q, want stable", got)
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

func dependencyNames(deps []cargoDependency) []string {
	out := make([]string, 0, len(deps))
	for _, dep := range deps {
		out = append(out, dep.Name)
	}
	return out
}

func dependencyKeys(deps []cargoDependency) []string {
	out := make([]string, 0, len(deps))
	for _, dep := range deps {
		out = append(out, dep.Key)
	}
	return out
}

func names(values []plan.DetectedValue) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, value.Name)
	}
	return out
}

func toolNames(values []plan.DetectedTool) []string {
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
		if item.Kind == plan.EvidenceConvention && item.Source == "rust-ecosystem" && item.Pointer == pointer {
			return true
		}
	}
	return false
}

func rustRequirementVersions(project plan.ProjectPlan) []string {
	var versions []string
	for _, requirement := range project.Requirements {
		if requirement.Name == "rust" {
			versions = append(versions, requirement.Version)
		}
	}
	return versions
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
