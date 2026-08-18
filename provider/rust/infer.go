package rust

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
		run:         "cargo fetch",
		pointer:     "/#install",
		capability:  plan.CapabilityDependenciesInstall,
		confidence:  plan.ConfidenceMedium,
		description: "Cargo packages conventionally download dependencies with cargo fetch.",
	},
	{
		name:        "test",
		run:         "cargo test",
		pointer:     "/#test",
		capability:  plan.CapabilityTestRun,
		confidence:  plan.ConfidenceHigh,
		description: "Cargo packages with tests conventionally run cargo test.",
		needsTests:  true,
	},
	{
		name:        "build",
		run:         "cargo build",
		pointer:     "/#build",
		capability:  plan.CapabilityArtifactBuild,
		confidence:  plan.ConfidenceMedium,
		description: "Cargo packages conventionally compile with cargo build.",
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
	source := ctx.SourcePath("Cargo.toml")
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
		Source:      "rust-ecosystem",
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
				Source:  "rust-ecosystem",
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
	case "/#test":
		return "test"
	case "/#build":
		return "build"
	default:
		return commandPointer
	}
}
