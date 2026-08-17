package node

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

func TestDetectReturnsNothingWithoutPackageJSON(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{"README.md": "hello\n"})
	if len(result.Findings) != 0 || len(result.Ambiguities) != 0 || len(result.Conflicts) != 0 {
		t.Fatalf("Detect() = %+v, want an empty result", result)
	}
}

func TestDetectReadsScriptsLockfileAndEngines(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"package.json": `{
			"name": "app",
			"packageManager": "npm@10.8.2",
			"engines": {"node": ">=20"},
			"scripts": {
				"test": "vitest run",
				"lint": "eslint src",
				"build": "tsc --noEmit"
			}
		}`,
		"package-lock.json": `{ "lockfileVersion": 3 }`,
		"tsconfig.json":     `{ "compilerOptions": { "strict": true } }`,
	})

	project := assembleProject(t, ".", result)

	if got := names(project.Languages); !slices.Equal(got, []string{"javascript", "typescript"}) {
		t.Fatalf("languages = %v, want javascript and typescript", got)
	}
	if len(project.PackageManagers) != 1 || project.PackageManagers[0].Name != "npm" || project.PackageManagers[0].Version != "10.8.2" {
		t.Fatalf("packageManagers = %+v, want npm 10.8.2", project.PackageManagers)
	}
	if len(project.Preparation) != 1 || deref(project.Preparation[0].Run) != "npm ci" {
		t.Fatalf("preparation = %+v, want npm ci", project.Preparation)
	}

	commands := commandRuns(project.Commands)
	if commands["test"] != "npm test" {
		t.Fatalf("test command = %q, want npm test", commands["test"])
	}
	if commands["lint"] != "npm run lint" {
		t.Fatalf("lint command = %q, want npm run lint", commands["lint"])
	}
	if commands["build"] != "npm run build" {
		t.Fatalf("build command = %q, want npm run build", commands["build"])
	}

	caps := commandCapabilities(project.Commands)
	if !slices.Contains(caps["test"], plan.CapabilityTestRun) {
		t.Fatalf("test interpretations = %v, want test.run", caps["test"])
	}
	if !slices.Contains(caps["lint"], plan.CapabilityCodeLint) {
		t.Fatalf("lint interpretations = %v, want code.lint", caps["lint"])
	}
	if !slices.Contains(caps["build"], plan.CapabilityCodeTypecheck) {
		t.Fatalf("build interpretations = %v, want code.typecheck", caps["build"])
	}

	if len(project.Requirements) != 1 || project.Requirements[0].Name != "node" || project.Requirements[0].Version != ">=20" {
		t.Fatalf("requirements = %+v, want node >=20", project.Requirements)
	}
}

func TestDetectReportsCompetingLockfilesAsAmbiguity(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"package.json":      `{"scripts": {"test": "vitest"}}`,
		"package-lock.json": `{}`,
		"pnpm-lock.yaml":    "lockfileVersion: '9.0'\n",
	})
	project := assembleProject(t, ".", result)

	if got := namesOfTools(project.PackageManagers); !slices.Equal(got, []string{"npm", "pnpm"}) {
		t.Fatalf("packageManagers = %v, want npm and pnpm", got)
	}
	if len(project.Preparation) != 0 {
		t.Fatalf("preparation = %+v, want none while the package manager is unresolved", project.Preparation)
	}
	if len(project.Commands) != 1 || project.Commands[0].Run != nil {
		t.Fatalf("commands = %+v, want a declared test with a null run", project.Commands)
	}

	subjects := ambiguitySubjects(project.Ambiguities)
	if !slices.Contains(subjects, "tool.package-manager") {
		t.Fatalf("ambiguities = %v, want tool.package-manager", subjects)
	}
	if !slices.Contains(subjects, "dependencies.install") {
		t.Fatalf("ambiguities = %v, want dependencies.install", subjects)
	}
	if !slices.Contains(subjects, "command.test.run") {
		t.Fatalf("ambiguities = %v, want command.test.run", subjects)
	}
}

func TestDetectResolvesPackageManagerFieldOverACompetingLockfile(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"package.json":      `{"packageManager": "pnpm@9.15.0", "scripts": {"build": "vite build"}}`,
		"pnpm-lock.yaml":    "lockfileVersion: '9.0'\n",
		"package-lock.json": `{}`,
		"vite.config.ts":    "export default {}\n",
	})
	project := assembleProject(t, ".", result)

	if deref(commandByName(t, project, "build").Run) != "pnpm run build" {
		t.Fatalf("build run = %v, want pnpm run build", commandByName(t, project, "build").Run)
	}
	if len(project.Preparation) != 1 || deref(project.Preparation[0].Run) != "pnpm install --frozen-lockfile" {
		t.Fatalf("preparation = %+v, want frozen pnpm install", project.Preparation)
	}
	if len(project.Conflicts) != 1 || project.Conflicts[0].Subject != "tool.package-manager" {
		t.Fatalf("conflicts = %+v, want a resolved package-manager conflict", project.Conflicts)
	}
	if project.Conflicts[0].Resolution == nil || project.Conflicts[0].Resolution.SelectedValue != "pnpm" {
		t.Fatalf("resolution = %+v, want pnpm", project.Conflicts[0].Resolution)
	}
	if got := names(project.Frameworks); !slices.Equal(got, []string{"vite"}) {
		t.Fatalf("frameworks = %v, want vite", got)
	}
}

func TestDetectReportsConfiguredToolsWithoutAMatchingCommand(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"package.json":     `{"name": "lib"}`,
		"eslint.config.js": "export default []\n",
		"tsconfig.json":    `{}\n`,
	})
	project := assembleProject(t, ".", result)

	configured := factValues(project.Facts, "tool.configured")
	if !slices.Contains(configured, "eslint") {
		t.Fatalf("facts = %+v, want tool.configured=eslint", project.Facts)
	}
	if !slices.Contains(configured, "tsc") {
		t.Fatalf("facts = %+v, want tool.configured=tsc", project.Facts)
	}
	if len(project.Commands) != 0 {
		t.Fatalf("commands = %+v, want none", project.Commands)
	}
}

func TestDetectReadsRuntimeVersionFiles(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"package.json":  `{"engines": {"node": ">=18"}}`,
		".nvmrc":        "22\n",
		".node-version": "22\n",
	})
	project := assembleProject(t, ".", result)

	if len(project.Requirements) != 2 {
		t.Fatalf("requirements = %+v, want node 22 from version files and >=18 from engines", project.Requirements)
	}
	pin := requirementByVersion(t, project.Requirements, "22")
	if len(pin.Evidence) != 2 {
		t.Fatalf("pin evidence = %+v, want .nvmrc and .node-version only", pin.Evidence)
	}
	engines := requirementByVersion(t, project.Requirements, ">=18")
	if len(engines.Evidence) != 1 || engines.Evidence[0].Pointer != "/engines/node" {
		t.Fatalf("engines evidence = %+v, want /engines/node", engines.Evidence)
	}
}

func TestDetectDoesNotAttachDisagreeingEnginesEvidenceToAFilePin(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"package.json": `{"engines": {"node": ">=22"}}`,
		".nvmrc":       "20.11.1\n",
	})
	project := assembleProject(t, ".", result)

	pin := requirementByVersion(t, project.Requirements, "20.11.1")
	for _, evidence := range pin.Evidence {
		if evidence.Pointer == "/engines/node" {
			t.Fatalf("pin evidence = %+v, engines must not attach to a different version", pin.Evidence)
		}
	}
	engines := requirementByVersion(t, project.Requirements, ">=22")
	if engines.Evidence[0].Pointer != "/engines/node" {
		t.Fatalf("engines requirement = %+v", engines)
	}
}

func TestDetectConflictsWhenRuntimePinsDisagree(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"package.json":  `{}`,
		".nvmrc":        "22\n",
		".node-version": "20\n",
	})
	project := assembleProject(t, ".", result)

	if len(project.Conflicts) != 1 || project.Conflicts[0].Subject != "runtime.node.version" {
		t.Fatalf("conflicts = %+v, want runtime.node.version", project.Conflicts)
	}
	if project.Conflicts[0].Resolution != nil {
		t.Fatalf("resolution = %+v, want none", project.Conflicts[0].Resolution)
	}
}

func TestDetectInfersNpmFromNpmrcWhenNoLockfileIsPresent(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"package.json": `{"engines": {"node": ">=22"}, "scripts": {"test": "xo && tsc --noEmit"}}`,
		".npmrc":       "package-lock=false\n",
	})
	project := assembleProject(t, ".", result)

	if len(project.PackageManagers) != 1 || project.PackageManagers[0].Name != "npm" {
		t.Fatalf("packageManagers = %+v, want npm", project.PackageManagers)
	}
	if got := project.PackageManagers[0].Evidence[0].Description; got != "The file sets npm's package-lock option to false." {
		t.Fatalf("npmrc evidence description = %q", got)
	}
	if len(project.Preparation) != 1 || deref(project.Preparation[0].Run) != "npm install" {
		t.Fatalf("preparation = %+v, want npm install without a lockfile", project.Preparation)
	}
	test := commandByName(t, project, "test")
	if deref(test.Run) != "npm test" {
		t.Fatalf("test run = %v, want npm test", test.Run)
	}
	if !slices.Contains(commandCapabilities(project.Commands)["test"], plan.CapabilityCodeTypecheck) {
		t.Fatalf("test interpretations = %v, want code.typecheck from tsc", commandCapabilities(project.Commands)["test"])
	}
}

func TestDetectUsesRepositoryRelativeSourcesForNestedProjects(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "frontend", "package.json"), `{"scripts": {"lint": "eslint ."}}`)
	writeFile(t, filepath.Join(root, "frontend", "package-lock.json"), `{}`)

	result, err := Provider{}.Detect(provider.Context{RepositoryRoot: root, ProjectPath: "frontend"})
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	project := assembleProject(t, "frontend", result)
	lint := commandByName(t, project, "lint")
	if lint.Directory != "frontend" {
		t.Fatalf("directory = %q, want frontend", lint.Directory)
	}
	if lint.Evidence[0].Source != "frontend/package.json" {
		t.Fatalf("evidence source = %q, want frontend/package.json", lint.Evidence[0].Source)
	}

	id, err := plan.NewCommandID(plan.CommandIdentity{
		ProjectPath: "frontend",
		Provider:    "node",
		Source:      "frontend/package.json",
		Pointer:     "/scripts/lint",
	})
	if err != nil {
		t.Fatalf("NewCommandID() error = %v", err)
	}
	if lint.ID != id {
		t.Fatalf("id = %q, want %q", lint.ID, id)
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

func namesOfTools(tools []plan.DetectedTool) []string {
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		out = append(out, tool.Name)
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

func ambiguitySubjects(ambiguities []plan.Ambiguity) []string {
	out := make([]string, 0, len(ambiguities))
	for _, ambiguity := range ambiguities {
		out = append(out, ambiguity.Subject)
	}
	return out
}

func requirementByVersion(t *testing.T, requirements []plan.Requirement, version string) plan.Requirement {
	t.Helper()
	for _, requirement := range requirements {
		if requirement.Name == "node" && requirement.Version == version {
			return requirement
		}
	}
	t.Fatalf("node %s not found in %+v", version, requirements)
	return plan.Requirement{}
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
