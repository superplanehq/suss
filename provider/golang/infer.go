package golang

import (
	"fmt"
	"slices"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

type inferredSpec struct {
	name        string
	run         string
	pointer     string
	capability  plan.Capability
	confidence  plan.Confidence
	description string
	needsTests  bool
}

var inferredSpecs = []inferredSpec{
	{
		name:        "download dependencies",
		run:         "go mod download",
		pointer:     "/#install",
		capability:  plan.CapabilityDependenciesInstall,
		confidence:  plan.ConfidenceMedium,
		description: "Go modules conventionally download dependencies with go mod download.",
	},
	{
		name:        "vet",
		run:         "go vet ./...",
		pointer:     "/#vet",
		capability:  plan.CapabilityCodeLint,
		confidence:  plan.ConfidenceMedium,
		description: "Go modules conventionally run go vet ./... to check packages.",
	},
	{
		name:        "test",
		run:         "go test ./...",
		pointer:     "/#test",
		capability:  plan.CapabilityTestRun,
		confidence:  plan.ConfidenceHigh,
		description: "Go modules with test files conventionally run all package tests with go test ./....",
		needsTests:  true,
	},
	{
		name:        "build",
		run:         "go build ./...",
		pointer:     "/#build",
		capability:  plan.CapabilityArtifactBuild,
		confidence:  plan.ConfidenceMedium,
		description: "Go modules conventionally compile packages with go build ./....",
	},
}

func inferredCommands(ctx provider.Context) ([]plan.Finding, error) {
	testFiles, err := findTestFiles(ctx.ProjectDir())
	if err != nil {
		return nil, fmt.Errorf("find test files: %w", err)
	}
	slices.Sort(testFiles)

	findings := make([]plan.Finding, 0, len(inferredSpecs))
	for _, spec := range inferredSpecs {
		if spec.needsTests && len(testFiles) == 0 {
			continue
		}
		command, err := inferredCommand(ctx, spec, testFiles)
		if err != nil {
			return nil, err
		}
		findings = append(findings, plan.CommandFinding{
			ProjectPath: ctx.ProjectPath,
			Detector:    providerName,
			Command:     command,
		})
	}
	return findings, nil
}

func inferredCommand(ctx provider.Context, spec inferredSpec, testFiles []string) (plan.Command, error) {
	source := ctx.SourcePath("go.mod")
	id, err := plan.NewCommandID(plan.CommandIdentity{
		ProjectPath: ctx.ProjectPath,
		Provider:    providerName,
		Source:      source,
		Pointer:     spec.pointer,
	})
	if err != nil {
		return plan.Command{}, err
	}

	convention := conventionPointer(spec.pointer)
	evidence := []plan.Evidence{{
		Kind:   plan.EvidenceFile,
		Source: source,
	}}
	if spec.needsTests && len(testFiles) > 0 {
		evidence = append(evidence, plan.Evidence{
			Kind:   plan.EvidenceFile,
			Source: ctx.SourcePath(testFiles[0]),
		})
	}
	evidence = append(evidence, plan.Evidence{
		Kind:        plan.EvidenceConvention,
		Source:      "go-ecosystem",
		Pointer:     convention,
		Description: spec.description,
	})

	return plan.Command{
		ID:         id,
		Name:       spec.name,
		Run:        stringPtr(spec.run),
		Directory:  ctx.ProjectPath,
		Scope:      plan.ScopeProject,
		Origin:     plan.CommandInferred,
		Confidence: spec.confidence,
		Evidence:   evidence,
		Interpretations: []plan.Interpretation{{
			Capability: spec.capability,
			Confidence: spec.confidence,
			Evidence: []plan.Evidence{{
				Kind:    plan.EvidenceConvention,
				Source:  "go-ecosystem",
				Pointer: convention,
			}},
		}},
		Variants: []plan.CommandVariant{},
	}, nil
}

func conventionPointer(commandPointer string) string {
	switch commandPointer {
	case "/#install":
		return "dependencies"
	case "/#vet":
		return "vet"
	case "/#test":
		return "test"
	case "/#build":
		return "build"
	default:
		return commandPointer
	}
}
