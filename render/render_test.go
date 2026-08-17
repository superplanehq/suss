package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/superplanehq/suss/plan"
)

func TestWriteReportsNoProjectRoots(t *testing.T) {
	t.Parallel()

	got := renderDocument(plan.NewDocument(nil), []string{"node"})
	if !strings.Contains(got, "Providers: node") {
		t.Fatalf("output %q, want providers", got)
	}
	if !strings.Contains(got, "No project roots were detected") {
		t.Fatalf("output %q, want an empty-repository explanation", got)
	}
}

func TestWriteRendersFixtureFactsOnUncoveredProjects(t *testing.T) {
	t.Parallel()

	project := plan.NewProjectPlan("testdata/sample")
	project.Facts = []plan.ProjectFact{{
		Name:       "project.role",
		Value:      "fixture",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceFile, Source: "testdata/sample"}},
	}}

	got := renderDocument(plan.NewDocument([]plan.ProjectPlan{project}), []string{"node"})
	if !strings.Contains(got, "No implemented provider produced findings") {
		t.Fatalf("output %q, want an uncovered-project explanation", got)
	}
	if !strings.Contains(got, "project.role: fixture") {
		t.Fatalf("output %q, want the fixture fact", got)
	}
	if !strings.Contains(got, "Evidence:") {
		t.Fatalf("output %q, want fixture evidence", got)
	}
	if strings.Contains(got, "Languages:") {
		t.Fatalf("output %q, want no empty field wall", got)
	}
}

func TestWriteRendersACoveredNodeProject(t *testing.T) {
	t.Parallel()

	run := "npm test"
	install := "npm ci"
	id := plan.CommandID("cmd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	installID := plan.CommandID("cmd_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	project := plan.NewProjectPlan(".")
	project.Languages = []plan.DetectedValue{{
		Name:       "javascript",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "package.json"}},
	}}
	project.PackageManagers = []plan.DetectedTool{{
		Name:       "npm",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceFile, Source: "package-lock.json"}},
	}}
	project.Facts = []plan.ProjectFact{{
		Name:       "tool.configured",
		Value:      "eslint",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceConfiguration, Source: "eslint.config.js"}},
	}}
	project.Preparation = []plan.Command{{
		ID:         installID,
		Name:       "install dependencies",
		Run:        &install,
		Directory:  ".",
		Scope:      plan.ScopeProject,
		Origin:     plan.CommandInferred,
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceFile, Source: "package-lock.json"}},
		Interpretations: []plan.Interpretation{{
			Capability: plan.CapabilityDependenciesInstall,
			Confidence: plan.ConfidenceHigh,
			Evidence:   []plan.Evidence{{Kind: plan.EvidenceConvention, Source: "node-ecosystem", Pointer: "npm-ci"}},
		}},
		Variants: []plan.CommandVariant{},
	}}
	project.Commands = []plan.Command{{
		ID:         id,
		Name:       "test",
		Run:        &run,
		Directory:  ".",
		Scope:      plan.ScopeProject,
		Origin:     plan.CommandDeclared,
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "package.json", Pointer: "/scripts/test"}},
		Interpretations: []plan.Interpretation{{
			Capability: plan.CapabilityTestRun,
			Confidence: plan.ConfidenceHigh,
			Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "package.json", Pointer: "/scripts/test"}},
		}},
		Variants: []plan.CommandVariant{{
			Context:    "ci",
			Run:        "npm test --coverage",
			Directory:  ".",
			Confidence: plan.ConfidenceHigh,
			Evidence:   []plan.Evidence{{Kind: plan.EvidenceInvocation, Source: ".github/workflows/ci.yml", Pointer: "/jobs/test/steps/2/run"}},
		}},
	}}

	got := renderDocument(plan.NewDocument([]plan.ProjectPlan{project}), []string{"node"})
	for _, want := range []string{
		"Providers: node",
		"Languages: javascript",
		"Package managers: npm",
		"npm ci",
		"npm test",
		"ci  npm test --coverage",
		"eslint is configured. No command interpreted as code.lint was found.",
		"package.json",
		"eslint.config.js",
		".github/workflows/ci.yml",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output %q, want %q", got, want)
		}
	}
}

func TestWriteListsInterpretedCommandsFirst(t *testing.T) {
	t.Parallel()

	unknown := "pnpm run e2e:harness:coverage"
	testRun := "pnpm run test"
	lintRun := "pnpm run lint"
	project := plan.NewProjectPlan(".")
	project.Languages = []plan.DetectedValue{{
		Name:       "javascript",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "package.json"}},
	}}
	project.Commands = []plan.Command{
		{
			ID:         plan.CommandID("cmd_11111111111111111111111111111111"),
			Name:       "e2e:harness:coverage",
			Run:        &unknown,
			Directory:  ".",
			Scope:      plan.ScopeProject,
			Origin:     plan.CommandDeclared,
			Confidence: plan.ConfidenceHigh,
			Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "package.json"}},
			Variants:   []plan.CommandVariant{},
		},
		{
			ID:         plan.CommandID("cmd_22222222222222222222222222222222"),
			Name:       "test",
			Run:        &testRun,
			Directory:  ".",
			Scope:      plan.ScopeProject,
			Origin:     plan.CommandDeclared,
			Confidence: plan.ConfidenceHigh,
			Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "package.json"}},
			Interpretations: []plan.Interpretation{{
				Capability: plan.CapabilityTestRun,
				Confidence: plan.ConfidenceHigh,
				Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "package.json"}},
			}},
			Variants: []plan.CommandVariant{},
		},
		{
			ID:         plan.CommandID("cmd_33333333333333333333333333333333"),
			Name:       "lint",
			Run:        &lintRun,
			Directory:  ".",
			Scope:      plan.ScopeProject,
			Origin:     plan.CommandDeclared,
			Confidence: plan.ConfidenceHigh,
			Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "package.json"}},
			Interpretations: []plan.Interpretation{{
				Capability: plan.CapabilityCodeLint,
				Confidence: plan.ConfidenceHigh,
				Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "package.json"}},
			}},
			Variants: []plan.CommandVariant{},
		},
	}

	got := renderDocument(plan.NewDocument([]plan.ProjectPlan{project}), []string{"node"})
	lintAt := strings.Index(got, "lint")
	testAt := strings.Index(got, "test")
	unknownAt := strings.Index(got, "e2e:harness:coverage")
	if lintAt < 0 || testAt < 0 || unknownAt < 0 {
		t.Fatalf("output %q, want lint, test, and e2e:harness:coverage", got)
	}
	if !(lintAt < testAt && testAt < unknownAt) {
		t.Fatalf("output %q, want interpreted commands before uninterpreted", got)
	}
}

func TestWriteKeepsMultilineCommandRunsOnOneLine(t *testing.T) {
	t.Parallel()

	run := "# into K buckets\n# suite leftover"
	project := plan.NewProjectPlan(".")
	project.Languages = []plan.DetectedValue{{
		Name:       "javascript",
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: "package.json"}},
	}}
	project.Commands = []plan.Command{{
		ID:         plan.CommandID("cmd_cccccccccccccccccccccccccccccccc"),
		Name:       "# into",
		Run:        &run,
		Directory:  ".",
		Scope:      plan.ScopeProject,
		Origin:     plan.CommandObserved,
		Confidence: plan.ConfidenceHigh,
		Evidence:   []plan.Evidence{{Kind: plan.EvidenceInvocation, Source: ".github/workflows/ci.yml"}},
		Variants:   []plan.CommandVariant{},
	}}

	got := renderDocument(plan.NewDocument([]plan.ProjectPlan{project}), []string{"node"})
	if strings.Contains(got, "\n# suite leftover") {
		t.Fatalf("output %q, want the run collapsed onto one line", got)
	}
	if !strings.Contains(got, "# into K buckets # suite leftover") {
		t.Fatalf("output %q, want collapsed run text", got)
	}
}

func renderDocument(document plan.Document, providers []string) string {
	var buf bytes.Buffer
	Write(&buf, document, Options{Providers: providers})
	return buf.String()
}
