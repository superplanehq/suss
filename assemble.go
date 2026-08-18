package suss

import (
	"github.com/superplanehq/suss/knowledge"
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
			project.Requirements = upsertRequirement(project.Requirements, item.Requirement)
		case plan.CommandFinding:
			if shouldPrepare(item) {
				project.Preparation = append(project.Preparation, item.Command)
			} else {
				project.Commands = append(project.Commands, item.Command)
			}
		}
	}
	project.Ambiguities = append(project.Ambiguities, result.Ambiguities...)
	project.Conflicts = append(project.Conflicts, result.Conflicts...)
	project.ApplyWorkspaceScope()
	return project
}

// shouldPrepare reports whether a command is preparation rather than a
// regular project command. The rule is capability-based, not detector-based:
// anything interpreted as dependencies.install (declared Make target, npm
// script, inferred go mod download, …) and any `docker compose up` belongs
// in preparation. Origin does not matter.
func shouldPrepare(item plan.CommandFinding) bool {
	return isComposeUpCommand(item.Command) || hasCapability(item.Command, plan.CapabilityDependenciesInstall)
}

func isComposeUpCommand(command plan.Command) bool {
	if command.Run == nil {
		return false
	}
	for _, inv := range knowledge.ParseScript(*command.Run) {
		if knowledge.IsComposeUp(inv) {
			return true
		}
	}
	return false
}

func upsertRequirement(requirements []plan.Requirement, incoming plan.Requirement) []plan.Requirement {
	for i, existing := range requirements {
		if !sameAssembledRequirement(existing, incoming) {
			continue
		}
		requirements[i].Evidence = mergeAssembledEvidence(existing.Evidence, incoming.Evidence)
		if incoming.Kind == plan.RequirementEnvironment {
			requirements[i].IsRequired = orBool(existing.IsRequired, incoming.IsRequired)
			requirements[i].HasDefault = orBool(existing.HasDefault, incoming.HasDefault)
		}
		return requirements
	}
	return append(requirements, incoming)
}

func sameAssembledRequirement(a, b plan.Requirement) bool {
	if a.Kind != b.Kind || a.Name != b.Name {
		return false
	}
	if a.Kind == plan.RequirementEnvironment {
		return true
	}
	return a.Version == b.Version
}

func mergeAssembledEvidence(existing, incoming []plan.Evidence) []plan.Evidence {
	out := append([]plan.Evidence{}, existing...)
	seen := make(map[string]struct{}, len(out))
	for _, item := range out {
		seen[string(item.Kind)+"\x00"+item.Source+"\x00"+item.Pointer+"\x00"+item.Description] = struct{}{}
	}
	for _, item := range incoming {
		key := string(item.Kind) + "\x00" + item.Source + "\x00" + item.Pointer + "\x00" + item.Description
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func orBool(a, b *bool) *bool {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	value := *a || *b
	return &value
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
