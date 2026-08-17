package reconcile

import (
	"testing"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

func TestApplyLinksCIFlagsAsAVariantOfADeclaredScript(t *testing.T) {
	t.Parallel()

	declared := command(t, "node", ".", "/scripts/test", "test", "pnpm run test", plan.CommandDeclared, plan.CapabilityTestRun)
	observed := command(t, "github-actions", ".", "/jobs/test/steps/2/run", "test", "pnpm test --run", plan.CommandObserved, plan.CapabilityTestRun)
	observed.Evidence = []plan.Evidence{{Kind: plan.EvidenceInvocation, Source: ".github/workflows/ci.yml", Pointer: "/jobs/test/steps/2/run"}}

	root := plan.NewProjectPlan(".")
	root.Commands = []plan.Command{declared}
	got := Apply([]plan.ProjectPlan{root}, provider.Result{
		Findings: []plan.Finding{plan.CommandFinding{Command: observed}},
	})

	if len(got[0].Commands) != 1 {
		t.Fatalf("commands = %+v, want the declared command only", got[0].Commands)
	}
	if len(got[0].Commands[0].Variants) != 1 || got[0].Commands[0].Variants[0].Run != "pnpm test --run" {
		t.Fatalf("variants = %+v, want pnpm test --run", got[0].Commands[0].Variants)
	}
	if got[0].Commands[0].Variants[0].Context != "ci" {
		t.Fatalf("variant context = %q, want ci", got[0].Commands[0].Variants[0].Context)
	}
}

func TestApplyLinksInstallFlagDifferencesAsVariants(t *testing.T) {
	t.Parallel()

	inferred := command(t, "node", ".", "/#install", "install dependencies", "yarn install --frozen-lockfile", plan.CommandInferred, plan.CapabilityDependenciesInstall)
	observed := command(t, "github-actions", ".", "/jobs/test/steps/1/run", "install dependencies", "yarn install", plan.CommandObserved, plan.CapabilityDependenciesInstall)

	root := plan.NewProjectPlan(".")
	root.Preparation = []plan.Command{inferred}
	got := Apply([]plan.ProjectPlan{root}, provider.Result{
		Findings: []plan.Finding{plan.CommandFinding{Command: observed}},
	})

	if len(got[0].Preparation) != 1 || len(got[0].Preparation[0].Variants) != 1 {
		t.Fatalf("preparation = %+v, want inferred install with a CI variant", got[0].Preparation)
	}
	if got[0].Preparation[0].Variants[0].Run != "yarn install" {
		t.Fatalf("variant run = %q, want yarn install", got[0].Preparation[0].Variants[0].Run)
	}
}

func TestApplyKeepsUnrelatedCICommandsObserved(t *testing.T) {
	t.Parallel()

	declared := command(t, "node", ".", "/scripts/test", "test", "yarn run test", plan.CommandDeclared, plan.CapabilityTestRun)
	observed := command(t, "github-actions", ".", "/jobs/docker/steps/0/run", "docker build", "docker build -t app .", plan.CommandObserved, "")

	root := plan.NewProjectPlan(".")
	root.Commands = []plan.Command{declared}
	got := Apply([]plan.ProjectPlan{root}, provider.Result{
		Findings: []plan.Finding{plan.CommandFinding{Command: observed}},
	})

	if len(got[0].Commands) != 2 {
		t.Fatalf("commands = %+v, want declared test and observed docker build", got[0].Commands)
	}
	var found bool
	for _, item := range got[0].Commands {
		if item.Origin == plan.CommandObserved && deref(item.Run) == "docker build -t app ." {
			found = true
		}
	}
	if !found {
		t.Fatalf("commands = %+v, want an observed docker build", got[0].Commands)
	}
}

func TestApplyRecordsPackageManagerMismatchAsConflict(t *testing.T) {
	t.Parallel()

	declared := command(t, "node", ".", "/scripts/test", "test", "yarn run test", plan.CommandDeclared, plan.CapabilityTestRun)
	observed := command(t, "github-actions", ".", "/jobs/test/steps/2/run", "test", "npm test", plan.CommandObserved, plan.CapabilityTestRun)

	root := plan.NewProjectPlan(".")
	root.Commands = []plan.Command{declared}
	got := Apply([]plan.ProjectPlan{root}, provider.Result{
		Findings: []plan.Finding{plan.CommandFinding{Command: observed}},
	})

	if len(got[0].Commands) != 1 {
		t.Fatalf("commands = %+v, want the declared command only", got[0].Commands)
	}
	if len(got[0].Conflicts) != 1 || got[0].Conflicts[0].Subject != "command.test.run" {
		t.Fatalf("conflicts = %+v, want command.test.run", got[0].Conflicts)
	}
	if got[0].Conflicts[0].CommandID == nil || *got[0].Conflicts[0].CommandID != declared.ID {
		t.Fatalf("conflict commandId = %v, want %s", got[0].Conflicts[0].CommandID, declared.ID)
	}
}

func TestApplyAssignsNestedWorkingDirectoryToTheNestedProject(t *testing.T) {
	t.Parallel()

	rootCmd := command(t, "node", ".", "/scripts/test", "test", "yarn run test", plan.CommandDeclared, plan.CapabilityTestRun)
	pkgCmd := command(t, "node", "packages/app", "/scripts/test", "test", "yarn run test", plan.CommandDeclared, plan.CapabilityTestRun)
	observed := command(t, "github-actions", "packages/app", "/jobs/test/steps/0/run", "test", "yarn test --watch=false", plan.CommandObserved, plan.CapabilityTestRun)

	root := plan.NewProjectPlan(".")
	root.Commands = []plan.Command{rootCmd}
	pkg := plan.NewProjectPlan("packages/app")
	pkg.Commands = []plan.Command{pkgCmd}

	got := Apply([]plan.ProjectPlan{root, pkg}, provider.Result{
		Findings: []plan.Finding{plan.CommandFinding{Command: observed}},
	})

	if len(got[0].Commands[0].Variants) != 0 {
		t.Fatalf("root variants = %+v, want none (no fan-out)", got[0].Commands[0].Variants)
	}
	if len(got[1].Commands[0].Variants) != 1 || got[1].Commands[0].Variants[0].Run != "yarn test --watch=false" {
		t.Fatalf("package variants = %+v, want yarn test --watch=false", got[1].Commands[0].Variants)
	}
}

func TestApplyMergesMatchingCIRuntimeEvidence(t *testing.T) {
	t.Parallel()

	root := plan.NewProjectPlan(".")
	root.Requirements = []plan.Requirement{{
		Kind:       plan.RequirementRuntime,
		Name:       "node",
		Version:    "22",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: ".nvmrc"}},
	}}
	got := Apply([]plan.ProjectPlan{root}, provider.Result{
		Findings: []plan.Finding{plan.RequirementFinding{
			ProjectPath: ".",
			Requirement: plan.Requirement{
				Kind:       plan.RequirementRuntime,
				Name:       "node",
				Version:    "22",
				Confidence: plan.ConfidenceHigh,
				Evidence:   []plan.Evidence{{Kind: plan.EvidenceInvocation, Source: ".github/workflows/ci.yml", Pointer: "/jobs/test/steps/1/with/node-version"}},
			},
		}},
	})

	if len(got[0].Requirements) != 1 || got[0].Requirements[0].Version != "22" {
		t.Fatalf("requirements = %+v, want node 22", got[0].Requirements)
	}
	if len(got[0].Requirements[0].Evidence) != 2 {
		t.Fatalf("evidence = %+v, want declaration and invocation", got[0].Requirements[0].Evidence)
	}
}

func TestApplyFoldsCIMatrixIntoADeclaredEngineRange(t *testing.T) {
	t.Parallel()

	root := plan.NewProjectPlan(".")
	root.Requirements = []plan.Requirement{{
		Kind:       plan.RequirementRuntime,
		Name:       "node",
		Version:    ">=22",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "package.json", Pointer: "/engines/node"}},
	}}
	got := Apply([]plan.ProjectPlan{root}, provider.Result{
		Findings: []plan.Finding{
			ciNode("22"),
			ciNode("26"),
		},
	})

	if len(got[0].Requirements) != 1 || got[0].Requirements[0].Version != ">=22" {
		t.Fatalf("requirements = %+v, want a single node >=22", got[0].Requirements)
	}
	if len(got[0].Requirements[0].Evidence) != 3 {
		t.Fatalf("evidence = %+v, want engines plus both matrix versions", got[0].Requirements[0].Evidence)
	}
	if len(got[0].Facts) != 0 {
		t.Fatalf("facts = %+v, want none; matrix versions corroborate the range", got[0].Facts)
	}
}

func TestApplyDoesNotAddMatrixVersionsAsExtraRequirementsOnAPin(t *testing.T) {
	t.Parallel()

	root := plan.NewProjectPlan(".")
	root.Requirements = []plan.Requirement{{
		Kind:       plan.RequirementRuntime,
		Name:       "node",
		Version:    "22",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: ".nvmrc"}},
	}}
	got := Apply([]plan.ProjectPlan{root}, provider.Result{
		Findings: []plan.Finding{
			ciNode("22"),
			ciNode("24"),
		},
	})

	if len(got[0].Requirements) != 1 || got[0].Requirements[0].Version != "22" {
		t.Fatalf("requirements = %+v, want node 22 only", got[0].Requirements)
	}
	if len(got[0].Requirements[0].Evidence) != 2 {
		t.Fatalf("evidence = %+v, want .nvmrc and the matching CI pin", got[0].Requirements[0].Evidence)
	}
	if values := factValues(got[0].Facts, "ci.matrix.node"); len(values) != 1 || values[0] != "24" {
		t.Fatalf("facts = %+v, want ci.matrix.node=24 for the extra matrix version", got[0].Facts)
	}
}

func TestApplyCollapsesAMatrixWithNoDeclarationToOneRuntime(t *testing.T) {
	t.Parallel()

	root := plan.NewProjectPlan(".")
	got := Apply([]plan.ProjectPlan{root}, provider.Result{
		Findings: []plan.Finding{
			ciNode("22"),
			ciNode("26"),
		},
	})

	if len(got[0].Requirements) != 1 || got[0].Requirements[0].Name != "node" || got[0].Requirements[0].Version != "" {
		t.Fatalf("requirements = %+v, want one unversioned node runtime", got[0].Requirements)
	}
	if values := factValues(got[0].Facts, "ci.matrix.node"); len(values) != 2 {
		t.Fatalf("facts = %v, want matrix versions 22 and 26", values)
	}
}

func TestApplyConflictsASingleCIPinAgainstADifferentDeclaration(t *testing.T) {
	t.Parallel()

	root := plan.NewProjectPlan(".")
	root.Requirements = []plan.Requirement{{
		Kind:       plan.RequirementRuntime,
		Name:       "node",
		Version:    "18",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: ".nvmrc"}},
	}}
	got := Apply([]plan.ProjectPlan{root}, provider.Result{
		Findings: []plan.Finding{ciNode("22")},
	})

	if len(got[0].Requirements) != 1 || got[0].Requirements[0].Version != "18" {
		t.Fatalf("requirements = %+v, want declared node 18", got[0].Requirements)
	}
	if len(got[0].Conflicts) != 1 || got[0].Conflicts[0].Subject != "runtime.node.version" {
		t.Fatalf("conflicts = %+v, want runtime.node.version", got[0].Conflicts)
	}
}

func ciNode(version string) plan.RequirementFinding {
	return plan.RequirementFinding{
		ProjectPath: ".",
		Requirement: plan.Requirement{
			Kind:       plan.RequirementRuntime,
			Name:       "node",
			Version:    version,
			Confidence: plan.ConfidenceHigh,
			Evidence:   []plan.Evidence{{Kind: plan.EvidenceInvocation, Source: ".github/workflows/ci.yml", Pointer: "/jobs/test/steps/1/with/node-version"}},
		},
	}
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

func command(t *testing.T, providerName, dir, pointer, name, run string, origin plan.CommandOrigin, capability plan.Capability) plan.Command {
	t.Helper()
	id, err := plan.NewCommandID(plan.CommandIdentity{
		ProjectPath: dir,
		Provider:    providerName,
		Source:      "source",
		Pointer:     pointer,
	})
	if err != nil {
		t.Fatalf("NewCommandID() error = %v", err)
	}
	item := plan.Command{
		ID:         id,
		Name:       name,
		Run:        stringPtr(run),
		Directory:  dir,
		Scope:      plan.ScopeProject,
		Origin:     origin,
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "package.json", Pointer: pointer}},
		Variants:   []plan.CommandVariant{},
	}
	if capability != "" {
		item.Interpretations = []plan.Interpretation{{
			Capability: capability,
			Confidence: plan.ConfidenceHigh,
			Evidence:   item.Evidence,
		}}
	}
	return item
}

func stringPtr(value string) *string {
	return &value
}
