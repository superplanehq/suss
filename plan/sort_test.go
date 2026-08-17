package plan

import (
	"slices"
	"testing"
)

func TestSortOrdersEveryArrayCanonically(t *testing.T) {
	t.Parallel()

	run := "pnpm test"
	commandID := CommandID("cmd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	otherID := CommandID("cmd_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")

	document := Document{
		SchemaVersion: SchemaVersion,
		Projects: []ProjectPlan{
			{
				Path: "frontend",
				Languages: []DetectedValue{
					{Name: "typescript", Evidence: []Evidence{{Kind: EvidenceFile, Source: "tsconfig.json"}}},
					{Name: "javascript", Evidence: []Evidence{{Kind: EvidenceFile, Source: "index.js"}}},
				},
				Frameworks: []DetectedValue{
					{Name: "vite", Evidence: []Evidence{{Kind: EvidenceConfiguration, Source: "vite.config.ts"}}},
					{Name: "react", Evidence: []Evidence{{Kind: EvidenceConfiguration, Source: "package.json"}}},
				},
				PackageManagers: []DetectedTool{
					{Name: "pnpm", Version: "9", Evidence: []Evidence{{Kind: EvidenceFile, Source: "pnpm-lock.yaml"}}},
					{Name: "npm", Evidence: []Evidence{{Kind: EvidenceFile, Source: "package-lock.json"}}},
				},
				Facts: []ProjectFact{
					{Name: "workspace.orchestrator", Value: "turbo", Evidence: []Evidence{{Kind: EvidenceFile, Source: "turbo.json"}}},
					{Name: "workspace.orchestrator", Value: "pnpm", Evidence: []Evidence{{Kind: EvidenceFile, Source: "pnpm-workspace.yaml"}}},
				},
				Requirements: []Requirement{
					{Kind: RequirementTool, Name: "pnpm", Evidence: []Evidence{{Kind: EvidenceDeclaration, Source: "package.json"}}},
					{Kind: RequirementRuntime, Name: "node", Version: "22", Evidence: []Evidence{{Kind: EvidenceDeclaration, Source: "package.json"}}},
					{Kind: RequirementRuntime, Name: "node", Version: "20", Evidence: []Evidence{{Kind: EvidenceDeclaration, Source: ".nvmrc"}}},
				},
				Preparation: []Command{
					commandWithID(otherID, "install"),
					commandWithID(commandID, "enable"),
				},
				Commands: []Command{
					{
						ID:         otherID,
						Name:       "lint",
						Run:        stringPtr("pnpm lint"),
						Directory:  ".",
						Scope:      ScopeProject,
						Origin:     CommandDeclared,
						Confidence: ConfidenceHigh,
						Evidence: []Evidence{
							{Kind: EvidenceFile, Source: "package.json", Pointer: "/scripts/lint"},
							{Kind: EvidenceDeclaration, Source: "package.json", Pointer: "/scripts/lint"},
						},
						Interpretations: []Interpretation{
							{Capability: CapabilityCodeLint, Evidence: []Evidence{{Kind: EvidenceConvention, Source: "node-ecosystem"}}},
							{Capability: CapabilityTestRun, Evidence: []Evidence{{Kind: EvidenceConvention, Source: "node-ecosystem"}}},
						},
						Variants: []CommandVariant{
							{Context: "ci", Run: "pnpm lint --max-warnings=0", Directory: "frontend", Evidence: []Evidence{{Kind: EvidenceInvocation, Source: "ci.yml"}}},
							{Context: "ci", Run: "pnpm lint", Directory: ".", Evidence: []Evidence{{Kind: EvidenceInvocation, Source: "ci.yml"}}},
						},
					},
					{
						ID:         commandID,
						Name:       "test",
						Run:        &run,
						Directory:  ".",
						Scope:      ScopeProject,
						Origin:     CommandDeclared,
						Confidence: ConfidenceHigh,
						Evidence:   []Evidence{{Kind: EvidenceDeclaration, Source: "package.json"}},
						Interpretations: []Interpretation{
							{Capability: CapabilityTestRun, Evidence: []Evidence{{Kind: EvidenceDeclaration, Source: "package.json"}}},
						},
						Variants: []CommandVariant{},
					},
				},
				Ambiguities: []Ambiguity{
					{
						Subject: "tool.package-manager",
						Message: "lockfiles disagree",
						Candidates: []Candidate{
							{Value: "pnpm", Evidence: []Evidence{{Kind: EvidenceFile, Source: "pnpm-lock.yaml"}}},
							{Value: "npm", Evidence: []Evidence{{Kind: EvidenceFile, Source: "package-lock.json"}}},
						},
					},
					{
						Subject:   "command.test.run",
						CommandID: &commandID,
						Message:   "invocation is unresolved",
						Candidates: []Candidate{
							{Value: "pnpm test", Evidence: []Evidence{{Kind: EvidenceFile, Source: "pnpm-lock.yaml"}}},
						},
					},
				},
				Conflicts: []Conflict{
					{
						Subject: "runtime.node.version",
						Message: "versions disagree",
						Assertions: []Candidate{
							{Value: "22", Evidence: []Evidence{{Kind: EvidenceDeclaration, Source: "package.json"}}},
							{Value: "20", Evidence: []Evidence{{Kind: EvidenceFile, Source: ".nvmrc"}}},
						},
					},
				},
			},
			NewProjectPlan("."),
		},
	}

	document.Sort()

	if got := projectPaths(document); !slices.Equal(got, []string{".", "frontend"}) {
		t.Fatalf("project paths = %v, want [., frontend]", got)
	}

	project := document.Projects[1]
	if got := detectedNames(project.Languages); !slices.Equal(got, []string{"javascript", "typescript"}) {
		t.Fatalf("languages = %v", got)
	}
	if got := detectedNames(project.Frameworks); !slices.Equal(got, []string{"react", "vite"}) {
		t.Fatalf("frameworks = %v", got)
	}
	if project.PackageManagers[0].Name != "npm" || project.PackageManagers[1].Name != "pnpm" {
		t.Fatalf("packageManagers = %+v", project.PackageManagers)
	}
	if project.Facts[0].Value != "pnpm" || project.Facts[1].Value != "turbo" {
		t.Fatalf("facts = %+v", project.Facts)
	}
	if project.Requirements[0].Kind != RequirementRuntime || project.Requirements[0].Version != "20" {
		t.Fatalf("requirements[0] = %+v", project.Requirements[0])
	}
	if project.Requirements[1].Kind != RequirementRuntime || project.Requirements[1].Version != "22" {
		t.Fatalf("requirements[1] = %+v", project.Requirements[1])
	}
	if project.Preparation[0].ID != commandID || project.Preparation[1].ID != otherID {
		t.Fatalf("preparation ids = %s, %s", project.Preparation[0].ID, project.Preparation[1].ID)
	}
	if project.Commands[0].ID != commandID || project.Commands[1].ID != otherID {
		t.Fatalf("command ids = %s, %s", project.Commands[0].ID, project.Commands[1].ID)
	}
	if project.Commands[1].Evidence[0].Kind != EvidenceDeclaration {
		t.Fatalf("command evidence kind = %s, want declaration first", project.Commands[1].Evidence[0].Kind)
	}
	if project.Commands[1].Interpretations[0].Capability != CapabilityCodeLint {
		t.Fatalf("interpretations[0] = %s, want code.lint", project.Commands[1].Interpretations[0].Capability)
	}
	if project.Commands[1].Variants[0].Directory != "." {
		t.Fatalf("variants[0].directory = %s, want .", project.Commands[1].Variants[0].Directory)
	}
	if project.Ambiguities[0].Subject != "command.test.run" {
		t.Fatalf("ambiguities[0].subject = %s", project.Ambiguities[0].Subject)
	}
	if project.Ambiguities[1].Candidates[0].Value != "npm" {
		t.Fatalf("candidates[0] = %s, want npm", project.Ambiguities[1].Candidates[0].Value)
	}
	if project.Conflicts[0].Assertions[0].Value != "20" {
		t.Fatalf("assertions[0] = %s, want 20", project.Conflicts[0].Assertions[0].Value)
	}
}

func commandWithID(id CommandID, name string) Command {
	run := name
	return Command{
		ID:              id,
		Name:            name,
		Run:             &run,
		Directory:       ".",
		Scope:           ScopeProject,
		Origin:          CommandDeclared,
		Confidence:      ConfidenceHigh,
		Evidence:        []Evidence{{Kind: EvidenceFile, Source: "package.json"}},
		Interpretations: []Interpretation{},
		Variants:        []CommandVariant{},
	}
}

func projectPaths(document Document) []string {
	paths := make([]string, len(document.Projects))
	for i, project := range document.Projects {
		paths[i] = project.Path
	}
	return paths
}

func detectedNames(values []DetectedValue) []string {
	names := make([]string, len(values))
	for i, value := range values {
		names[i] = value.Name
	}
	return names
}

func stringPtr(value string) *string {
	return &value
}
