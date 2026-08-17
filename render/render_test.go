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

func TestWriteDistinguishesUncoveredProjects(t *testing.T) {
	t.Parallel()

	document := plan.NewDocument([]plan.ProjectPlan{plan.NewProjectPlan("backend")})
	got := renderDocument(document, []string{"node"})
	if !strings.Contains(got, "No implemented provider produced findings") {
		t.Fatalf("output %q, want an uncovered-project explanation", got)
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
		Variants: []plan.CommandVariant{},
	}}

	got := renderDocument(plan.NewDocument([]plan.ProjectPlan{project}), []string{"node"})
	for _, want := range []string{
		"Providers: node",
		"Languages: javascript",
		"Package managers: npm",
		"npm ci",
		"npm test",
		"eslint is configured. No command interpreted as code.lint was found.",
		"package.json",
		"eslint.config.js",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output %q, want %q", got, want)
		}
	}
}

func renderDocument(document plan.Document, providers []string) string {
	var buf bytes.Buffer
	Write(&buf, document, Options{Providers: providers})
	return buf.String()
}
