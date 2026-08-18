package reconcile

import (
	"slices"
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

func TestApplyLinksGoTestFlagsAsAVariantAndRaisesConfidence(t *testing.T) {
	t.Parallel()

	inferred := command(t, "go", ".", "/#test", "test", "go test ./...", plan.CommandInferred, plan.CapabilityTestRun)
	inferred.Confidence = plan.ConfidenceMedium
	observed := command(t, "github-actions", ".", "/jobs/test/steps/2/run", "go test", "go test -v -short -race ./...", plan.CommandObserved, plan.CapabilityTestRun)
	observed.Evidence = []plan.Evidence{{Kind: plan.EvidenceInvocation, Source: ".github/workflows/ci.yml", Pointer: "/jobs/test/steps/2/run"}}

	root := plan.NewProjectPlan(".")
	root.Commands = []plan.Command{inferred}
	got := Apply([]plan.ProjectPlan{root}, provider.Result{
		Findings: []plan.Finding{plan.CommandFinding{Command: observed}},
	})

	if len(got[0].Commands) != 1 {
		t.Fatalf("commands = %+v, want the inferred command only", got[0].Commands)
	}
	if got[0].Commands[0].Confidence != plan.ConfidenceHigh {
		t.Fatalf("confidence = %s, want high after CI confirmation", got[0].Commands[0].Confidence)
	}
	if len(got[0].Commands[0].Variants) != 1 || got[0].Commands[0].Variants[0].Run != "go test -v -short -race ./..." {
		t.Fatalf("variants = %+v, want go test -v -short -race ./...", got[0].Commands[0].Variants)
	}
	if len(got[0].Commands[0].Evidence) < 2 {
		t.Fatalf("evidence = %+v, want convention plus CI invocation", got[0].Commands[0].Evidence)
	}
}

func TestPreferDeclaredDropsInferredConventionCommands(t *testing.T) {
	t.Parallel()

	declared := command(t, "make", ".", "/targets/test", "test", "make test", plan.CommandDeclared, plan.CapabilityTestRun)
	inferred := command(t, "go", ".", "/#test", "test", "go test ./...", plan.CommandInferred, plan.CapabilityTestRun)
	vet := command(t, "go", ".", "/#vet", "vet", "go vet ./...", plan.CommandInferred, plan.CapabilityCodeLint)

	root := plan.NewProjectPlan(".")
	root.Commands = []plan.Command{declared, inferred, vet}
	got := PreferDeclared(root)

	var names []string
	for _, command := range got.Commands {
		names = append(names, deref(command.Run))
	}
	if !slices.Contains(names, "make test") {
		t.Fatalf("commands = %v, want make test kept", names)
	}
	if slices.Contains(names, "go test ./...") {
		t.Fatalf("commands = %v, did not want inferred go test beside make test", names)
	}
	if !slices.Contains(names, "go vet ./...") {
		t.Fatalf("commands = %v, want inferred go vet when no declared lint exists", names)
	}
}

func TestApplyPutsComposeUpInPreparation(t *testing.T) {
	t.Parallel()

	observed := command(t, "github-actions", ".", "/jobs/test/steps/0/run", "docker compose", "docker compose up -d", plan.CommandObserved, "")
	root := plan.NewProjectPlan(".")
	got := Apply([]plan.ProjectPlan{root}, provider.Result{
		Findings: []plan.Finding{plan.CommandFinding{Command: observed}},
	})

	if len(got[0].Preparation) != 1 || deref(got[0].Preparation[0].Run) != "docker compose up -d" {
		t.Fatalf("preparation = %+v, want observed docker compose up -d", got[0].Preparation)
	}
	if len(got[0].Commands) != 0 {
		t.Fatalf("commands = %+v, did not want compose up as a regular command", got[0].Commands)
	}
}

func TestApplyLinksCIComposeUpAsAVariant(t *testing.T) {
	t.Parallel()

	inferred := command(t, "compose", ".", "/#up", "start services", "docker compose up -d", plan.CommandInferred, "")
	observed := command(t, "github-actions", ".", "/jobs/test/steps/0/run", "docker compose", "docker compose up -d postgres", plan.CommandObserved, "")

	root := plan.NewProjectPlan(".")
	root.Preparation = []plan.Command{inferred}
	got := Apply([]plan.ProjectPlan{root}, provider.Result{
		Findings: []plan.Finding{plan.CommandFinding{Command: observed}},
	})

	if len(got[0].Preparation) != 1 || len(got[0].Preparation[0].Variants) != 1 {
		t.Fatalf("preparation = %+v, want inferred compose up with a CI variant", got[0].Preparation)
	}
	if got[0].Preparation[0].Variants[0].Run != "docker compose up -d postgres" {
		t.Fatalf("variant run = %q, want docker compose up -d postgres", got[0].Preparation[0].Variants[0].Run)
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

func TestApplyFoldsNewerCIToolchainIntoCargoMSRV(t *testing.T) {
	t.Parallel()

	root := plan.NewProjectPlan(".")
	root.Requirements = []plan.Requirement{{
		Kind:       plan.RequirementRuntime,
		Name:       "rust",
		Version:    ">=1.74",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "Cargo.toml", Pointer: "/package/rust-version"}},
	}}
	got := Apply([]plan.ProjectPlan{root}, provider.Result{
		Findings: []plan.Finding{plan.RequirementFinding{
			ProjectPath: ".",
			Requirement: plan.Requirement{
				Kind:       plan.RequirementRuntime,
				Name:       "rust",
				Version:    "1.81.0",
				Confidence: plan.ConfidenceHigh,
				Evidence:   []plan.Evidence{{Kind: plan.EvidenceInvocation, Source: ".github/workflows/ci.yml", Pointer: "/jobs/test/steps/1/uses"}},
			},
		}},
	})

	if len(got[0].Requirements) != 1 || got[0].Requirements[0].Version != ">=1.74" {
		t.Fatalf("requirements = %+v, want rust >=1.74", got[0].Requirements)
	}
	if len(got[0].Requirements[0].Evidence) != 2 {
		t.Fatalf("evidence = %+v, want MSRV plus CI toolchain", got[0].Requirements[0].Evidence)
	}
	if len(got[0].Conflicts) != 0 {
		t.Fatalf("conflicts = %+v, did not want a false MSRV conflict", got[0].Conflicts)
	}
}

func TestApplyConflictsCIPinAgainstExactRustWhenMSRVWouldAccept(t *testing.T) {
	t.Parallel()

	root := plan.NewProjectPlan(".")
	root.Requirements = []plan.Requirement{
		{
			Kind:       plan.RequirementRuntime,
			Name:       "rust",
			Version:    "1.81.0",
			Confidence: plan.ConfidenceHigh,
			Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "rust-toolchain.toml"}},
		},
		{
			Kind:       plan.RequirementRuntime,
			Name:       "rust",
			Version:    ">=1.74",
			Confidence: plan.ConfidenceHigh,
			Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "Cargo.toml", Pointer: "/package/rust-version"}},
		},
	}
	got := Apply([]plan.ProjectPlan{root}, provider.Result{
		Findings: []plan.Finding{plan.RequirementFinding{
			ProjectPath: ".",
			Requirement: plan.Requirement{
				Kind:       plan.RequirementRuntime,
				Name:       "rust",
				Version:    "1.82.0",
				Confidence: plan.ConfidenceHigh,
				Evidence:   []plan.Evidence{{Kind: plan.EvidenceInvocation, Source: ".github/workflows/ci.yml", Pointer: "/jobs/test/steps/1/uses"}},
			},
		}},
	})

	if len(got[0].Conflicts) != 1 || got[0].Conflicts[0].Subject != "runtime.rust.version" {
		t.Fatalf("conflicts = %+v, want CI 1.82.0 to contradict the exact 1.81.0 pin", got[0].Conflicts)
	}
	if got[0].Conflicts[0].Assertions[0].Value != "1.81.0" || got[0].Conflicts[0].Assertions[1].Value != "1.82.0" {
		t.Fatalf("assertions = %+v, want 1.81.0 vs 1.82.0", got[0].Conflicts[0].Assertions)
	}
	if len(got[0].Requirements) != 2 {
		t.Fatalf("requirements = %+v, want the exact pin and the MSRV", got[0].Requirements)
	}
}

func TestApplyRecordsMatrixExtraAgainstExactRustInsteadOfFoldingIntoMSRV(t *testing.T) {
	t.Parallel()

	root := plan.NewProjectPlan(".")
	root.Requirements = []plan.Requirement{
		{
			Kind:       plan.RequirementRuntime,
			Name:       "rust",
			Version:    "1.81.0",
			Confidence: plan.ConfidenceHigh,
			Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "rust-toolchain.toml"}},
		},
		{
			Kind:       plan.RequirementRuntime,
			Name:       "rust",
			Version:    ">=1.74",
			Confidence: plan.ConfidenceHigh,
			Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "Cargo.toml", Pointer: "/package/rust-version"}},
		},
	}
	got := Apply([]plan.ProjectPlan{root}, provider.Result{
		Findings: []plan.Finding{
			plan.RequirementFinding{
				ProjectPath: ".",
				Requirement: plan.Requirement{
					Kind:       plan.RequirementRuntime,
					Name:       "rust",
					Version:    "1.81.0",
					Confidence: plan.ConfidenceHigh,
					Evidence:   []plan.Evidence{{Kind: plan.EvidenceInvocation, Source: ".github/workflows/ci.yml", Pointer: "/jobs/test/steps/1/with/toolchain"}},
				},
			},
			plan.RequirementFinding{
				ProjectPath: ".",
				Requirement: plan.Requirement{
					Kind:       plan.RequirementRuntime,
					Name:       "rust",
					Version:    "1.82.0",
					Confidence: plan.ConfidenceHigh,
					Evidence:   []plan.Evidence{{Kind: plan.EvidenceInvocation, Source: ".github/workflows/ci.yml", Pointer: "/jobs/test/steps/1/with/toolchain"}},
				},
			},
		},
	})

	if len(got[0].Conflicts) != 0 {
		t.Fatalf("conflicts = %+v, want matrix extras recorded as facts", got[0].Conflicts)
	}
	var rustVersions []string
	for _, requirement := range got[0].Requirements {
		if requirement.Name == "rust" {
			rustVersions = append(rustVersions, requirement.Version)
		}
	}
	if !slices.Equal(rustVersions, []string{"1.81.0", ">=1.74"}) {
		t.Fatalf("rust versions = %v, want exact pin plus MSRV", rustVersions)
	}
	var extras []string
	for _, fact := range got[0].Facts {
		if fact.Name == "ci.matrix.rust" {
			extras = append(extras, fact.Value)
		}
	}
	if !slices.Equal(extras, []string{"1.82.0"}) {
		t.Fatalf("facts = %+v, want ci.matrix.rust=1.82.0", got[0].Facts)
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
	if values := matrixNodeValues(got[0].Facts); len(values) != 1 || values[0] != "24" {
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
	if values := matrixNodeValues(got[0].Facts); len(values) != 2 {
		t.Fatalf("facts = %v, want matrix versions 22 and 26", values)
	}
}

func TestApplyDoesNotFoldIncompatibleRangeMembersAsEvidence(t *testing.T) {
	t.Parallel()

	root := plan.NewProjectPlan(".")
	root.Requirements = []plan.Requirement{{
		Kind:       plan.RequirementRuntime,
		Name:       "node",
		Version:    "^18",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "package.json", Pointer: "/engines/node"}},
	}}
	got := Apply([]plan.ProjectPlan{root}, provider.Result{
		Findings: []plan.Finding{ciNode("22")},
	})

	if len(got[0].Requirements) != 1 || got[0].Requirements[0].Version != "^18" {
		t.Fatalf("requirements = %+v, want declared ^18", got[0].Requirements)
	}
	if len(got[0].Requirements[0].Evidence) != 1 {
		t.Fatalf("evidence = %+v, did not want CI 22 attached to ^18", got[0].Requirements[0].Evidence)
	}
	if len(got[0].Conflicts) != 1 {
		t.Fatalf("conflicts = %+v, want a version conflict for 22 vs ^18", got[0].Conflicts)
	}
}

func TestApplyDoesNotFoldSetupPHPExcludedByComposerInequality(t *testing.T) {
	t.Parallel()

	root := plan.NewProjectPlan(".")
	root.Requirements = []plan.Requirement{{
		Kind:       plan.RequirementRuntime,
		Name:       "php",
		Version:    "!=8.1",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "composer.json", Pointer: "/require/php"}},
	}}
	got := Apply([]plan.ProjectPlan{root}, provider.Result{
		Findings: []plan.Finding{ciPHP("8.1.0")},
	})

	if len(got[0].Requirements[0].Evidence) != 1 {
		t.Fatalf("evidence = %+v, did not want CI 8.1.0 attached to !=8.1", got[0].Requirements[0].Evidence)
	}
	if len(got[0].Conflicts) != 1 {
		t.Fatalf("conflicts = %+v, want a version conflict for 8.1.0 vs !=8.1", got[0].Conflicts)
	}
}

func TestApplyDoesNotFoldSetupPHPExcludedByComposerWildcardInequality(t *testing.T) {
	t.Parallel()

	root := plan.NewProjectPlan(".")
	root.Requirements = []plan.Requirement{{
		Kind:       plan.RequirementRuntime,
		Name:       "php",
		Version:    "!=8.1.*",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "composer.json", Pointer: "/require/php"}},
	}}
	got := Apply([]plan.ProjectPlan{root}, provider.Result{
		Findings: []plan.Finding{ciPHP("8.1.5")},
	})

	if len(got[0].Requirements[0].Evidence) != 1 {
		t.Fatalf("evidence = %+v, did not want CI 8.1.5 attached to !=8.1.*", got[0].Requirements[0].Evidence)
	}
	if len(got[0].Conflicts) != 1 {
		t.Fatalf("conflicts = %+v, want a version conflict for 8.1.5 vs !=8.1.*", got[0].Conflicts)
	}
}

func TestApplyFoldsSetupPHPInsideAComposerSinglePipeUnion(t *testing.T) {
	t.Parallel()

	root := plan.NewProjectPlan(".")
	root.Requirements = []plan.Requirement{{
		Kind:       plan.RequirementRuntime,
		Name:       "php",
		Version:    "^8.1 | ^8.3",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "composer.json", Pointer: "/require/php"}},
	}}
	got := Apply([]plan.ProjectPlan{root}, provider.Result{
		Findings: []plan.Finding{ciPHP("8.3")},
	})

	if len(got[0].Conflicts) != 0 {
		t.Fatalf("conflicts = %+v, want none for 8.3 vs ^8.1 | ^8.3", got[0].Conflicts)
	}
	if len(got[0].Requirements[0].Evidence) != 2 {
		t.Fatalf("evidence = %+v, want declaration plus CI 8.3", got[0].Requirements[0].Evidence)
	}
	if values := matrixPHPValues(got[0].Facts); len(values) != 0 {
		t.Fatalf("facts = %+v, did not want ci.matrix.php for an evaluable union", got[0].Facts)
	}
}

func TestApplyFoldsSetupPHPMatchingAComposerExactConstraint(t *testing.T) {
	t.Parallel()

	root := plan.NewProjectPlan(".")
	root.Requirements = []plan.Requirement{{
		Kind:       plan.RequirementRuntime,
		Name:       "php",
		Version:    "=8.3.0",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "composer.json", Pointer: "/require/php"}},
	}}
	got := Apply([]plan.ProjectPlan{root}, provider.Result{
		Findings: []plan.Finding{ciPHP("8.3.0")},
	})

	if len(got[0].Conflicts) != 0 {
		t.Fatalf("conflicts = %+v, want none for =8.3.0 vs 8.3.0", got[0].Conflicts)
	}
	if len(got[0].Requirements[0].Evidence) != 2 {
		t.Fatalf("evidence = %+v, want declaration plus CI 8.3.0", got[0].Requirements[0].Evidence)
	}
}

func TestApplyFoldsSetupPHPMatchingABareComposerExactVersion(t *testing.T) {
	t.Parallel()

	root := plan.NewProjectPlan(".")
	root.Requirements = []plan.Requirement{{
		Kind:       plan.RequirementRuntime,
		Name:       "php",
		Version:    "8.3",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "composer.json", Pointer: "/require/php"}},
	}}
	got := Apply([]plan.ProjectPlan{root}, provider.Result{
		Findings: []plan.Finding{ciPHP("8.3.0")},
	})

	if len(got[0].Conflicts) != 0 {
		t.Fatalf("conflicts = %+v, want none for 8.3 vs 8.3.0", got[0].Conflicts)
	}
	if len(got[0].Requirements[0].Evidence) != 2 {
		t.Fatalf("evidence = %+v, want declaration plus CI 8.3.0", got[0].Requirements[0].Evidence)
	}
}

func TestApplyDoesNotFoldSetupPHPBelowAStabilitySuffixedBound(t *testing.T) {
	t.Parallel()

	root := plan.NewProjectPlan(".")
	root.Requirements = []plan.Requirement{{
		Kind:       plan.RequirementRuntime,
		Name:       "php",
		Version:    ">=8.1@dev",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "composer.json", Pointer: "/require/php"}},
	}}
	got := Apply([]plan.ProjectPlan{root}, provider.Result{
		Findings: []plan.Finding{ciPHP("8.0")},
	})

	if len(got[0].Requirements[0].Evidence) != 1 {
		t.Fatalf("evidence = %+v, did not want CI 8.0 attached to >=8.1@dev", got[0].Requirements[0].Evidence)
	}
	if len(got[0].Conflicts) != 1 {
		t.Fatalf("conflicts = %+v, want a version conflict for 8.0 vs >=8.1@dev", got[0].Conflicts)
	}
}

func TestApplyFoldsSetupPHPInsideAComposerTildeRange(t *testing.T) {
	t.Parallel()

	root := plan.NewProjectPlan(".")
	root.Requirements = []plan.Requirement{{
		Kind:       plan.RequirementRuntime,
		Name:       "php",
		Version:    "~8.1",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "composer.json", Pointer: "/require/php"}},
	}}
	got := Apply([]plan.ProjectPlan{root}, provider.Result{
		Findings: []plan.Finding{ciPHP("8.2")},
	})

	if len(got[0].Conflicts) != 0 {
		t.Fatalf("conflicts = %+v, want none for Composer ~8.1 vs 8.2", got[0].Conflicts)
	}
	if len(got[0].Requirements[0].Evidence) != 2 {
		t.Fatalf("evidence = %+v, want declaration plus CI 8.2", got[0].Requirements[0].Evidence)
	}
}

func TestApplyDoesNotFoldSetupPHPOutsideAComposerCommaUpperBound(t *testing.T) {
	t.Parallel()

	root := plan.NewProjectPlan(".")
	root.Requirements = []plan.Requirement{{
		Kind:       plan.RequirementRuntime,
		Name:       "php",
		Version:    ">=8.1,<8.3",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "composer.json", Pointer: "/require/php"}},
	}}
	got := Apply([]plan.ProjectPlan{root}, provider.Result{
		Findings: []plan.Finding{ciPHP("8.4")},
	})

	if len(got[0].Requirements[0].Evidence) != 1 {
		t.Fatalf("evidence = %+v, did not want CI 8.4 attached to >=8.1,<8.3", got[0].Requirements[0].Evidence)
	}
	if len(got[0].Conflicts) != 1 {
		t.Fatalf("conflicts = %+v, want a version conflict for 8.4 vs >=8.1,<8.3", got[0].Conflicts)
	}
}

func TestApplyDoesNotFoldACIPinOutsideAComposerCommaConstraint(t *testing.T) {
	t.Parallel()

	root := plan.NewProjectPlan(".")
	root.Requirements = []plan.Requirement{{
		Kind:       plan.RequirementRuntime,
		Name:       "php",
		Version:    ">=8.1,<8.4",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "composer.json", Pointer: "/require/php"}},
	}}
	got := Apply([]plan.ProjectPlan{root}, provider.Result{
		Findings: []plan.Finding{ciPHP("8.4")},
	})

	if len(got[0].Requirements[0].Evidence) != 1 {
		t.Fatalf("evidence = %+v, did not want CI 8.4 attached to >=8.1,<8.4", got[0].Requirements[0].Evidence)
	}
	if len(got[0].Conflicts) != 1 {
		t.Fatalf("conflicts = %+v, want a version conflict for 8.4 vs >=8.1,<8.4", got[0].Conflicts)
	}
}

func TestApplyFoldsACIPinInsideAComposerCommaConstraint(t *testing.T) {
	t.Parallel()

	root := plan.NewProjectPlan(".")
	root.Requirements = []plan.Requirement{{
		Kind:       plan.RequirementRuntime,
		Name:       "php",
		Version:    ">=8.1,<8.4",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "composer.json", Pointer: "/require/php"}},
	}}
	got := Apply([]plan.ProjectPlan{root}, provider.Result{
		Findings: []plan.Finding{ciPHP("8.3")},
	})

	if len(got[0].Conflicts) != 0 {
		t.Fatalf("conflicts = %+v, want none for 8.3 vs >=8.1,<8.4", got[0].Conflicts)
	}
	if len(got[0].Requirements[0].Evidence) != 2 {
		t.Fatalf("evidence = %+v, want declaration plus CI 8.3", got[0].Requirements[0].Evidence)
	}
}

func TestApplyDoesNotFoldAVersionAboveAnUpperBound(t *testing.T) {
	t.Parallel()

	root := plan.NewProjectPlan(".")
	root.Requirements = []plan.Requirement{{
		Kind:       plan.RequirementRuntime,
		Name:       "node",
		Version:    ">=22 <25",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "package.json", Pointer: "/engines/node"}},
	}}
	got := Apply([]plan.ProjectPlan{root}, provider.Result{
		Findings: []plan.Finding{ciNode("26")},
	})

	if len(got[0].Requirements[0].Evidence) != 1 {
		t.Fatalf("evidence = %+v, did not want CI 26 attached to >=22 <25", got[0].Requirements[0].Evidence)
	}
	if len(got[0].Conflicts) != 1 {
		t.Fatalf("conflicts = %+v, want a version conflict for 26 vs >=22 <25", got[0].Conflicts)
	}
}

func TestApplyRecordsUnevaluableRangesAsMatrixFacts(t *testing.T) {
	t.Parallel()

	root := plan.NewProjectPlan(".")
	root.Requirements = []plan.Requirement{{
		Kind:       plan.RequirementRuntime,
		Name:       "node",
		Version:    "~",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "package.json", Pointer: "/engines/node"}},
	}}
	got := Apply([]plan.ProjectPlan{root}, provider.Result{
		Findings: []plan.Finding{ciNode("22")},
	})

	if values := matrixNodeValues(got[0].Facts); len(values) != 1 || values[0] != "22" {
		t.Fatalf("facts = %+v, want ci.matrix.node=22 for an unevaluable range", got[0].Facts)
	}
	if len(got[0].Requirements[0].Evidence) != 1 {
		t.Fatalf("evidence = %+v, did not want CI attached to an unevaluable declaration", got[0].Requirements[0].Evidence)
	}
}

func TestApplyMergesDuplicateVariantEvidence(t *testing.T) {
	t.Parallel()

	declared := command(t, "node", ".", "/scripts/test", "test", "pnpm run test", plan.CommandDeclared, plan.CapabilityTestRun)
	first := command(t, "github-actions", ".", "/jobs/alpha/steps/0/run", "test", "pnpm test", plan.CommandObserved, plan.CapabilityTestRun)
	first.Evidence = []plan.Evidence{{Kind: plan.EvidenceInvocation, Source: ".github/workflows/ci.yml", Pointer: "/jobs/alpha/steps/0/run"}}
	second := command(t, "github-actions", ".", "/jobs/zebra/steps/0/run", "test", "pnpm test", plan.CommandObserved, plan.CapabilityTestRun)
	second.Evidence = []plan.Evidence{{Kind: plan.EvidenceInvocation, Source: ".github/workflows/ci.yml", Pointer: "/jobs/zebra/steps/0/run"}}

	root := plan.NewProjectPlan(".")
	root.Commands = []plan.Command{declared}
	got := Apply([]plan.ProjectPlan{root}, provider.Result{
		Findings: []plan.Finding{
			plan.CommandFinding{Command: first},
			plan.CommandFinding{Command: second},
		},
	})

	if len(got[0].Commands[0].Variants) != 1 {
		t.Fatalf("variants = %+v, want one merged variant", got[0].Commands[0].Variants)
	}
	if len(got[0].Commands[0].Variants[0].Evidence) != 2 {
		t.Fatalf("variant evidence = %+v, want both job pointers", got[0].Commands[0].Variants[0].Evidence)
	}
}

func TestApplyDeduplicatesMergedRequirementEvidence(t *testing.T) {
	t.Parallel()

	root := plan.NewProjectPlan(".")
	root.Requirements = []plan.Requirement{{
		Kind:       plan.RequirementRuntime,
		Name:       "node",
		Version:    "24.16.0",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: ".node-version"}},
	}}
	observation := plan.Requirement{
		Kind:       plan.RequirementRuntime,
		Name:       "node",
		Version:    "24.16.0",
		Confidence: plan.ConfidenceHigh,
		Evidence: []plan.Evidence{
			{Kind: plan.EvidenceInvocation, Source: ".github/workflows/ci.yml", Pointer: "/jobs/a/steps/0/with/node-version-file"},
			{Kind: plan.EvidenceDeclaration, Source: ".node-version"},
		},
	}
	got := Apply([]plan.ProjectPlan{root}, provider.Result{
		Findings: []plan.Finding{
			plan.RequirementFinding{ProjectPath: ".", Requirement: observation},
			plan.RequirementFinding{ProjectPath: ".", Requirement: observation},
		},
	})

	if len(got[0].Requirements[0].Evidence) != 2 {
		t.Fatalf("evidence = %+v, want declaration plus one invocation", got[0].Requirements[0].Evidence)
	}
}

func TestApplyKeepsALonePinWhenAnotherSetupIsUnversioned(t *testing.T) {
	t.Parallel()

	root := plan.NewProjectPlan(".")
	got := Apply([]plan.ProjectPlan{root}, provider.Result{
		Findings: []plan.Finding{
			ciNode(""),
			ciNode("22"),
		},
	})

	if len(got[0].Requirements) != 1 || got[0].Requirements[0].Version != "22" {
		t.Fatalf("requirements = %+v, want node 22", got[0].Requirements)
	}
	if len(got[0].Requirements[0].Evidence) != 2 {
		t.Fatalf("evidence = %+v, want both setup observations", got[0].Requirements[0].Evidence)
	}
}

func TestApplyDoesNotConflictWhenCIOmitsAVersion(t *testing.T) {
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
		Findings: []plan.Finding{ciNode("")},
	})

	if len(got[0].Conflicts) != 0 {
		t.Fatalf("conflicts = %+v, want none for an unpinned setup", got[0].Conflicts)
	}
	if len(got[0].Requirements) != 1 || got[0].Requirements[0].Version != "22" {
		t.Fatalf("requirements = %+v, want declared node 22", got[0].Requirements)
	}
	if values := matrixNodeValues(got[0].Facts); len(values) != 0 {
		t.Fatalf("facts = %v, want none for an empty CI version", values)
	}
}

func TestApplyTreatsACIVersionAliasAsUnevaluable(t *testing.T) {
	t.Parallel()

	root := plan.NewProjectPlan(".")
	root.Requirements = []plan.Requirement{{
		Kind:       plan.RequirementRuntime,
		Name:       "node",
		Version:    ">=20",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "package.json", Pointer: "/engines/node"}},
	}}
	got := Apply([]plan.ProjectPlan{root}, provider.Result{
		Findings: []plan.Finding{ciNode("lts/*")},
	})

	if len(got[0].Conflicts) != 0 {
		t.Fatalf("conflicts = %+v, want none for a non-numeric alias", got[0].Conflicts)
	}
	if len(got[0].Requirements[0].Evidence) != 1 {
		t.Fatalf("evidence = %+v, did not want lts/* attached to >=20", got[0].Requirements[0].Evidence)
	}
	if values := matrixNodeValues(got[0].Facts); len(values) != 1 || values[0] != "lts/*" {
		t.Fatalf("facts = %+v, want ci.matrix.node=lts/*", got[0].Facts)
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

func ciPHP(version string) plan.RequirementFinding {
	return plan.RequirementFinding{
		ProjectPath: ".",
		Requirement: plan.Requirement{
			Kind:       plan.RequirementRuntime,
			Name:       "php",
			Version:    version,
			Confidence: plan.ConfidenceHigh,
			Evidence: []plan.Evidence{{
				Kind:        plan.EvidenceInvocation,
				Source:      ".github/workflows/ci.yml",
				Pointer:     "/jobs/test/steps/1/with/php-version",
				Description: "CI tests php " + version,
			}},
		},
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
			Evidence: []plan.Evidence{{
				Kind:        plan.EvidenceInvocation,
				Source:      ".github/workflows/ci.yml",
				Pointer:     "/jobs/test/steps/1/with/node-version",
				Description: "CI tests node " + version,
			}},
		},
	}
}

func matrixNodeValues(facts []plan.ProjectFact) []string {
	return matrixRuntimeValues(facts, "node")
}

func matrixPHPValues(facts []plan.ProjectFact) []string {
	return matrixRuntimeValues(facts, "php")
}

func matrixRuntimeValues(facts []plan.ProjectFact, runtime string) []string {
	var values []string
	for _, fact := range facts {
		if fact.Name == "ci.matrix."+runtime {
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
