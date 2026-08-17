// Package plan defines the versioned machine-readable output contract.
package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const (
	// SchemaVersion is the major version of the serialized plan contract.
	SchemaVersion = "1"

	commandIDDomain = "suss.command.v1"
)

var providerNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// Document is the versioned machine-readable output produced by Suss.
type Document struct {
	SchemaVersion string        `json:"schemaVersion"`
	Projects      []ProjectPlan `json:"projects"`
}

// NewDocument creates a document whose required collection fields encode as
// JSON arrays, including when no projects have been detected.
func NewDocument(projects []ProjectPlan) Document {
	if projects == nil {
		projects = make([]ProjectPlan, 0)
	}

	return Document{
		SchemaVersion: SchemaVersion,
		Projects:      projects,
	}
}

// ProjectPlan describes one detected project root. Paths are slash-separated
// and relative to the inspected repository; "." denotes its root.
type ProjectPlan struct {
	Path            string          `json:"path"`
	Languages       []DetectedValue `json:"languages"`
	Frameworks      []DetectedValue `json:"frameworks"`
	PackageManagers []DetectedTool  `json:"packageManagers"`
	Facts           []ProjectFact   `json:"facts"`
	Requirements    []Requirement   `json:"requirements"`
	Preparation     []Command       `json:"preparation"`
	Commands        []Command       `json:"commands"`
	Ambiguities     []Ambiguity     `json:"ambiguities"`
	Conflicts       []Conflict      `json:"conflicts"`
}

// NewProjectPlan creates an empty project plan with every required collection
// initialized.
func NewProjectPlan(path string) ProjectPlan {
	return ProjectPlan{
		Path:            path,
		Languages:       make([]DetectedValue, 0),
		Frameworks:      make([]DetectedValue, 0),
		PackageManagers: make([]DetectedTool, 0),
		Facts:           make([]ProjectFact, 0),
		Requirements:    make([]Requirement, 0),
		Preparation:     make([]Command, 0),
		Commands:        make([]Command, 0),
		Ambiguities:     make([]Ambiguity, 0),
		Conflicts:       make([]Conflict, 0),
	}
}

// Confidence expresses the strength of a conclusion, independently from how
// the underlying command or fact was discovered.
type Confidence string

const (
	ConfidenceHigh   Confidence = "high"
	ConfidenceMedium Confidence = "medium"
	ConfidenceLow    Confidence = "low"
)

// EvidenceKind describes the role a source played in a conclusion.
type EvidenceKind string

const (
	EvidenceFile          EvidenceKind = "file"
	EvidenceDeclaration   EvidenceKind = "declaration"
	EvidenceInvocation    EvidenceKind = "invocation"
	EvidenceConfiguration EvidenceKind = "configuration"
	EvidenceConvention    EvidenceKind = "convention"
)

// Evidence points to a stable, structured source. Pointer is a logical
// location within that source rather than a line number.
type Evidence struct {
	Kind        EvidenceKind `json:"kind"`
	Source      string       `json:"source"`
	Pointer     string       `json:"pointer,omitempty"`
	Description string       `json:"description,omitempty"`
}

// DetectedValue is an evidence-backed language or framework name.
type DetectedValue struct {
	Name       string     `json:"name"`
	Confidence Confidence `json:"confidence"`
	Evidence   []Evidence `json:"evidence"`
}

// DetectedTool is an evidence-backed package manager. Version is omitted when
// the repository does not constrain it.
type DetectedTool struct {
	Name       string     `json:"name"`
	Version    string     `json:"version,omitempty"`
	Confidence Confidence `json:"confidence"`
	Evidence   []Evidence `json:"evidence"`
}

// ProjectFact records extensible, namespaced project metadata such as a
// workspace orchestrator.
type ProjectFact struct {
	Name       string     `json:"name"`
	Value      string     `json:"value"`
	Confidence Confidence `json:"confidence"`
	Evidence   []Evidence `json:"evidence"`
}

// RequirementKind separates environmental facts that have different
// semantics.
type RequirementKind string

const (
	RequirementRuntime     RequirementKind = "runtime"
	RequirementTool        RequirementKind = "tool"
	RequirementService     RequirementKind = "service"
	RequirementEnvironment RequirementKind = "environment"
)

// Requirement is an environmental fact. IsRequired and HasDefault apply only
// to environment-variable requirements; values are intentionally never stored.
type Requirement struct {
	Kind       RequirementKind `json:"kind"`
	Name       string          `json:"name"`
	Version    string          `json:"version,omitempty"`
	IsRequired *bool           `json:"isRequired,omitempty"`
	HasDefault *bool           `json:"hasDefault,omitempty"`
	Confidence Confidence      `json:"confidence"`
	Evidence   []Evidence      `json:"evidence"`
}

// CommandID is stable while a command's source identity remains stable.
type CommandID string

// CommandOrigin distinguishes repository declarations and observations from
// convention-generated commands. It is deliberately separate from confidence.
type CommandOrigin string

const (
	CommandDeclared CommandOrigin = "declared"
	CommandObserved CommandOrigin = "observed"
	CommandInferred CommandOrigin = "inferred"
)

// CommandScope indicates whether a command applies to one project or the
// repository as a whole.
type CommandScope string

const (
	ScopeProject    CommandScope = "project"
	ScopeRepository CommandScope = "repository"
)

// Command is a repository-native or inferred task. Run is nil only when the
// task is declared but its package-manager invocation is ambiguous.
type Command struct {
	ID              CommandID        `json:"id"`
	Name            string           `json:"name"`
	Run             *string          `json:"run"`
	Directory       string           `json:"directory"`
	Scope           CommandScope     `json:"scope"`
	Origin          CommandOrigin    `json:"origin"`
	Confidence      Confidence       `json:"confidence"`
	Evidence        []Evidence       `json:"evidence"`
	Interpretations []Interpretation `json:"interpretations"`
	Variants        []CommandVariant `json:"variants"`
}

// CommandVariant preserves a contextual invocation linked to a primary
// command. Context is namespaced and starts with "ci" in v0.
type CommandVariant struct {
	Context    string     `json:"context"`
	Run        string     `json:"run"`
	Directory  string     `json:"directory"`
	Confidence Confidence `json:"confidence"`
	Evidence   []Evidence `json:"evidence"`
}

// Capability is a small normalized vocabulary layered on top of native task
// names.
type Capability string

const (
	CapabilityDependenciesInstall Capability = "dependencies.install"
	CapabilityArtifactBuild       Capability = "artifact.build"
	CapabilityTestRun             Capability = "test.run"
	CapabilityCodeLint            Capability = "code.lint"
	CapabilityCodeFormat          Capability = "code.format"
	CapabilityCodeTypecheck       Capability = "code.typecheck"
	CapabilityApplicationRun      Capability = "application.run"
)

// Interpretation is an optional evidence-backed meaning assigned to a
// command.
type Interpretation struct {
	Capability Capability `json:"capability"`
	Confidence Confidence `json:"confidence"`
	Evidence   []Evidence `json:"evidence"`
}

// Candidate is one possible answer to an unresolved question or one assertion
// participating in a conflict.
type Candidate struct {
	Value       string     `json:"value"`
	Description string     `json:"description,omitempty"`
	Evidence    []Evidence `json:"evidence"`
}

// Ambiguity records multiple plausible answers when Suss cannot select one.
type Ambiguity struct {
	Subject    string      `json:"subject"`
	CommandID  *CommandID  `json:"commandId,omitempty"`
	Message    string      `json:"message"`
	Candidates []Candidate `json:"candidates"`
}

// Resolution records why one conflicting assertion was selected.
type Resolution struct {
	SelectedValue string     `json:"selectedValue"`
	Reason        string     `json:"reason"`
	Confidence    Confidence `json:"confidence"`
	Evidence      []Evidence `json:"evidence"`
}

// Conflict records contradictory structured evidence. Resolution is omitted
// when the evidence does not justify selecting an assertion.
type Conflict struct {
	Subject    string      `json:"subject"`
	CommandID  *CommandID  `json:"commandId,omitempty"`
	Message    string      `json:"message"`
	Assertions []Candidate `json:"assertions"`
	Resolution *Resolution `json:"resolution,omitempty"`
}

// PropertyKind identifies the project property carried by a provider finding.
type PropertyKind string

const (
	PropertyLanguage       PropertyKind = "language"
	PropertyFramework      PropertyKind = "framework"
	PropertyPackageManager PropertyKind = "package-manager"
	PropertyFact           PropertyKind = "fact"
)

// Property is the normalized property payload emitted by a provider.
type Property struct {
	Kind       PropertyKind `json:"kind"`
	Name       string       `json:"name"`
	Value      string       `json:"value,omitempty"`
	Version    string       `json:"version,omitempty"`
	Confidence Confidence   `json:"confidence"`
	Evidence   []Evidence   `json:"evidence"`
}

// Finding is the closed set of evidence-backed values providers may emit.
// Findings are reconciler input and are not part of the serialized document.
type Finding interface {
	findingKind() FindingKind
}

// FindingKind identifies the payload carried by a Finding.
type FindingKind string

const (
	FindingProperty    FindingKind = "property"
	FindingRequirement FindingKind = "requirement"
	FindingCommand     FindingKind = "command"
)

// PropertyFinding carries one project-property observation.
type PropertyFinding struct {
	ProjectPath string
	Detector    string
	Property    Property
}

func (PropertyFinding) findingKind() FindingKind {
	return FindingProperty
}

// RequirementFinding carries one environmental requirement observation.
type RequirementFinding struct {
	ProjectPath string
	Detector    string
	Requirement Requirement
}

func (RequirementFinding) findingKind() FindingKind {
	return FindingRequirement
}

// CommandFinding carries one command declaration, observation, or inference.
type CommandFinding struct {
	ProjectPath string
	Detector    string
	Command     Command
}

func (CommandFinding) findingKind() FindingKind {
	return FindingCommand
}

// CommandIdentity names the structured source location that owns a command.
// Run text is intentionally absent so editing a task body does not change its
// identifier.
type CommandIdentity struct {
	ProjectPath string
	Provider    string
	Source      string
	Pointer     string
}

// NewCommandID returns "cmd_" plus the first 128 bits of a domain-separated
// SHA-256 digest over the command's source identity.
func NewCommandID(identity CommandIdentity) (CommandID, error) {
	if err := validateCommandIdentity(identity); err != nil {
		return "", err
	}

	payload := strings.Join([]string{
		commandIDDomain,
		identity.ProjectPath,
		identity.Provider,
		identity.Source,
		identity.Pointer,
	}, "\x00")
	digest := sha256.Sum256([]byte(payload))

	return CommandID("cmd_" + hex.EncodeToString(digest[:16])), nil
}

func validateCommandIdentity(identity CommandIdentity) error {
	fields := []struct {
		name  string
		value string
	}{
		{name: "project path", value: identity.ProjectPath},
		{name: "provider", value: identity.Provider},
		{name: "source", value: identity.Source},
		{name: "pointer", value: identity.Pointer},
	}

	for _, field := range fields {
		if field.value == "" {
			return fmt.Errorf("command identity %s must not be empty", field.name)
		}
		if strings.ContainsRune(field.value, '\x00') {
			return fmt.Errorf("command identity %s must not contain NUL", field.name)
		}
	}

	if !providerNamePattern.MatchString(identity.Provider) {
		return errors.New("command identity provider must use lowercase kebab-case")
	}

	return nil
}
