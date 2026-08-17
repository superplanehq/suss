package suss

import (
	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

func assemble(path string, result provider.Result) plan.ProjectPlan {
	project := plan.NewProjectPlan(path)
	for _, finding := range result.Findings {
		switch item := finding.(type) {
		case plan.PropertyFinding:
			applyProperty(&project, item.Property)
		case plan.RequirementFinding:
			project.Requirements = append(project.Requirements, item.Requirement)
		case plan.CommandFinding:
			if item.Command.Origin == plan.CommandInferred && hasCapability(item.Command, plan.CapabilityDependenciesInstall) {
				project.Preparation = append(project.Preparation, item.Command)
			} else {
				project.Commands = append(project.Commands, item.Command)
			}
		}
	}
	project.Ambiguities = append(project.Ambiguities, result.Ambiguities...)
	project.Conflicts = append(project.Conflicts, result.Conflicts...)
	return project
}

func applyProperty(project *plan.ProjectPlan, property plan.Property) {
	switch property.Kind {
	case plan.PropertyLanguage:
		project.Languages = append(project.Languages, plan.DetectedValue{
			Name:       property.Name,
			Confidence: property.Confidence,
			Evidence:   property.Evidence,
		})
	case plan.PropertyFramework:
		project.Frameworks = append(project.Frameworks, plan.DetectedValue{
			Name:       property.Name,
			Confidence: property.Confidence,
			Evidence:   property.Evidence,
		})
	case plan.PropertyPackageManager:
		project.PackageManagers = append(project.PackageManagers, plan.DetectedTool{
			Name:       property.Name,
			Version:    property.Version,
			Confidence: property.Confidence,
			Evidence:   property.Evidence,
		})
	case plan.PropertyFact:
		project.Facts = append(project.Facts, plan.ProjectFact{
			Name:       property.Name,
			Value:      property.Value,
			Confidence: property.Confidence,
			Evidence:   property.Evidence,
		})
	}
}

func hasCapability(command plan.Command, capability plan.Capability) bool {
	for _, interpretation := range command.Interpretations {
		if interpretation.Capability == capability {
			return true
		}
	}
	return false
}
