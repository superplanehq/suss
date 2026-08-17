package plan

import (
	"strings"
	"testing"
)

func TestValidateAcceptsNullRunWhenAmbiguityReferencesCommand(t *testing.T) {
	t.Parallel()

	document := documentWithNullRun(t, CommandDeclared, true)
	if err := document.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsNullRunWithoutAmbiguity(t *testing.T) {
	t.Parallel()

	document := documentWithNullRun(t, CommandDeclared, false)
	err := document.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want a missing ambiguity error")
	}
	if !strings.Contains(err.Error(), "not referenced by an ambiguity") {
		t.Fatalf("Validate() error = %v, want a missing ambiguity error", err)
	}
}

func TestValidateRejectsNullRunOnInferredCommand(t *testing.T) {
	t.Parallel()

	document := documentWithNullRun(t, CommandInferred, true)
	err := document.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want an origin error")
	}
	if !strings.Contains(err.Error(), "null run is only legal on declared commands") {
		t.Fatalf("Validate() error = %v, want an origin error", err)
	}
}

func TestValidateRejectsUnknownCommandReference(t *testing.T) {
	t.Parallel()

	document := NewDocument([]ProjectPlan{NewProjectPlan(".")})
	unknown := CommandID("cmd_ffffffffffffffffffffffffffffffff")
	document.Projects[0].Ambiguities = []Ambiguity{{
		Subject:    "command.test.run",
		CommandID:  &unknown,
		Message:    "missing command",
		Candidates: []Candidate{{Value: "npm test", Evidence: []Evidence{{Kind: EvidenceFile, Source: "package.json"}}}},
	}}

	err := document.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want an unknown command error")
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("Validate() error = %v, want an unknown command error", err)
	}
}

func TestMarshalCanonicalAllowsNullRun(t *testing.T) {
	t.Parallel()

	document := documentWithNullRun(t, CommandDeclared, true)
	encoded, err := document.MarshalCanonical()
	if err != nil {
		t.Fatalf("MarshalCanonical() error = %v", err)
	}
	if !strings.Contains(string(encoded), `"run": null`) {
		t.Fatalf("MarshalCanonical() = %s, want a JSON null run", encoded)
	}
	if strings.Contains(string(encoded), `"languages": null`) {
		t.Fatalf("MarshalCanonical() = %s, collections must stay arrays", encoded)
	}
}

func documentWithNullRun(t *testing.T, origin CommandOrigin, withAmbiguity bool) Document {
	t.Helper()

	id, err := NewCommandID(CommandIdentity{
		ProjectPath: ".",
		Provider:    "node",
		Source:      "package.json",
		Pointer:     "/scripts/test",
	})
	if err != nil {
		t.Fatalf("NewCommandID() error = %v", err)
	}

	project := NewProjectPlan(".")
	project.Commands = []Command{{
		ID:              id,
		Name:            "test",
		Run:             nil,
		Directory:       ".",
		Scope:           ScopeProject,
		Origin:          origin,
		Confidence:      ConfidenceHigh,
		Evidence:        []Evidence{{Kind: EvidenceDeclaration, Source: "package.json", Pointer: "/scripts/test"}},
		Interpretations: []Interpretation{},
		Variants:        []CommandVariant{},
	}}
	if withAmbiguity {
		project.Ambiguities = []Ambiguity{{
			Subject:   "command.test.run",
			CommandID: &id,
			Message:   "The test script is declared, but its package-manager invocation cannot be selected.",
			Candidates: []Candidate{{
				Value:    "npm test",
				Evidence: []Evidence{{Kind: EvidenceFile, Source: "package-lock.json"}},
			}},
		}}
	}

	return NewDocument([]ProjectPlan{project})
}
