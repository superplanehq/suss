package plan

import (
	"cmp"
	"slices"
)

// Sort puts every array in canonical emit order so golden snapshots are
// byte-stable. Nil collections are replaced with empty slices.
//
// Ordering:
//  1. projects by path
//  2. languages and frameworks by name
//  3. package managers by name, then version
//  4. facts by name, then value
//  5. requirements by kind (runtime, tool, service, environment), then name, then version
//  6. preparation and commands by id
//  7. interpretations by capability
//  8. variants by context, then run, then directory
//  9. evidence by kind (declaration, invocation, configuration, file, convention), then source, then pointer, then description
//  10. ambiguities and conflicts by subject, then commandId (absent first), then message
//  11. candidates and assertions by value
func (d *Document) Sort() {
	if d.Projects == nil {
		d.Projects = make([]ProjectPlan, 0)
	}

	for i := range d.Projects {
		d.Projects[i].sort()
	}

	slices.SortFunc(d.Projects, func(a, b ProjectPlan) int {
		return cmp.Compare(a.Path, b.Path)
	})
}

func (p *ProjectPlan) sort() {
	p.Languages = sortedDetectedValues(p.Languages)
	p.Frameworks = sortedDetectedValues(p.Frameworks)
	p.PackageManagers = sortedDetectedTools(p.PackageManagers)
	p.Facts = sortedFacts(p.Facts)
	p.Requirements = sortedRequirements(p.Requirements)
	p.Preparation = sortedCommands(p.Preparation)
	p.Commands = sortedCommands(p.Commands)
	p.Ambiguities = sortedAmbiguities(p.Ambiguities)
	p.Conflicts = sortedConflicts(p.Conflicts)
}

func sortedDetectedValues(values []DetectedValue) []DetectedValue {
	values = cloneOrEmpty(values)
	for i := range values {
		values[i].Evidence = sortedEvidence(values[i].Evidence)
	}
	slices.SortFunc(values, func(a, b DetectedValue) int {
		return cmp.Compare(a.Name, b.Name)
	})
	return values
}

func sortedDetectedTools(tools []DetectedTool) []DetectedTool {
	tools = cloneOrEmpty(tools)
	for i := range tools {
		tools[i].Evidence = sortedEvidence(tools[i].Evidence)
	}
	slices.SortFunc(tools, func(a, b DetectedTool) int {
		if n := cmp.Compare(a.Name, b.Name); n != 0 {
			return n
		}
		return cmp.Compare(a.Version, b.Version)
	})
	return tools
}

func sortedFacts(facts []ProjectFact) []ProjectFact {
	facts = cloneOrEmpty(facts)
	for i := range facts {
		facts[i].Evidence = sortedEvidence(facts[i].Evidence)
	}
	slices.SortFunc(facts, func(a, b ProjectFact) int {
		if n := cmp.Compare(a.Name, b.Name); n != 0 {
			return n
		}
		return cmp.Compare(a.Value, b.Value)
	})
	return facts
}

func sortedRequirements(requirements []Requirement) []Requirement {
	requirements = cloneOrEmpty(requirements)
	for i := range requirements {
		requirements[i].Evidence = sortedEvidence(requirements[i].Evidence)
	}
	slices.SortFunc(requirements, func(a, b Requirement) int {
		if n := compareRequirementKind(a.Kind, b.Kind); n != 0 {
			return n
		}
		if n := cmp.Compare(a.Name, b.Name); n != 0 {
			return n
		}
		return cmp.Compare(a.Version, b.Version)
	})
	return requirements
}

func sortedCommands(commands []Command) []Command {
	commands = cloneOrEmpty(commands)
	for i := range commands {
		commands[i].Evidence = sortedEvidence(commands[i].Evidence)
		commands[i].Interpretations = sortedInterpretations(commands[i].Interpretations)
		commands[i].Variants = sortedVariants(commands[i].Variants)
	}
	slices.SortFunc(commands, func(a, b Command) int {
		return cmp.Compare(string(a.ID), string(b.ID))
	})
	return commands
}

func sortedInterpretations(interpretations []Interpretation) []Interpretation {
	interpretations = cloneOrEmpty(interpretations)
	for i := range interpretations {
		interpretations[i].Evidence = sortedEvidence(interpretations[i].Evidence)
	}
	slices.SortFunc(interpretations, func(a, b Interpretation) int {
		return cmp.Compare(string(a.Capability), string(b.Capability))
	})
	return interpretations
}

func sortedVariants(variants []CommandVariant) []CommandVariant {
	variants = cloneOrEmpty(variants)
	for i := range variants {
		variants[i].Evidence = sortedEvidence(variants[i].Evidence)
	}
	slices.SortFunc(variants, func(a, b CommandVariant) int {
		if n := cmp.Compare(a.Context, b.Context); n != 0 {
			return n
		}
		if n := cmp.Compare(a.Run, b.Run); n != 0 {
			return n
		}
		return cmp.Compare(a.Directory, b.Directory)
	})
	return variants
}

func sortedEvidence(evidence []Evidence) []Evidence {
	evidence = cloneOrEmpty(evidence)
	slices.SortFunc(evidence, func(a, b Evidence) int {
		if n := compareEvidenceKind(a.Kind, b.Kind); n != 0 {
			return n
		}
		if n := cmp.Compare(a.Source, b.Source); n != 0 {
			return n
		}
		if n := cmp.Compare(a.Pointer, b.Pointer); n != 0 {
			return n
		}
		return cmp.Compare(a.Description, b.Description)
	})
	return evidence
}

func sortedAmbiguities(ambiguities []Ambiguity) []Ambiguity {
	ambiguities = cloneOrEmpty(ambiguities)
	for i := range ambiguities {
		ambiguities[i].Candidates = sortedCandidates(ambiguities[i].Candidates)
	}
	slices.SortFunc(ambiguities, func(a, b Ambiguity) int {
		if n := cmp.Compare(a.Subject, b.Subject); n != 0 {
			return n
		}
		if n := compareOptionalCommandID(a.CommandID, b.CommandID); n != 0 {
			return n
		}
		return cmp.Compare(a.Message, b.Message)
	})
	return ambiguities
}

func sortedConflicts(conflicts []Conflict) []Conflict {
	conflicts = cloneOrEmpty(conflicts)
	for i := range conflicts {
		conflicts[i].Assertions = sortedCandidates(conflicts[i].Assertions)
		if conflicts[i].Resolution != nil {
			conflicts[i].Resolution.Evidence = sortedEvidence(conflicts[i].Resolution.Evidence)
		}
	}
	slices.SortFunc(conflicts, func(a, b Conflict) int {
		if n := cmp.Compare(a.Subject, b.Subject); n != 0 {
			return n
		}
		if n := compareOptionalCommandID(a.CommandID, b.CommandID); n != 0 {
			return n
		}
		return cmp.Compare(a.Message, b.Message)
	})
	return conflicts
}

func sortedCandidates(candidates []Candidate) []Candidate {
	candidates = cloneOrEmpty(candidates)
	for i := range candidates {
		candidates[i].Evidence = sortedEvidence(candidates[i].Evidence)
	}
	slices.SortFunc(candidates, func(a, b Candidate) int {
		return cmp.Compare(a.Value, b.Value)
	})
	return candidates
}

func cloneOrEmpty[T any](values []T) []T {
	if values == nil {
		return make([]T, 0)
	}
	cloned := make([]T, len(values))
	copy(cloned, values)
	return cloned
}

func compareOptionalCommandID(a, b *CommandID) int {
	return cmp.Compare(commandIDValue(a), commandIDValue(b))
}

func commandIDValue(id *CommandID) string {
	if id == nil {
		return ""
	}
	return string(*id)
}

var evidenceKindOrder = map[EvidenceKind]int{
	EvidenceDeclaration:   0,
	EvidenceInvocation:    1,
	EvidenceConfiguration: 2,
	EvidenceFile:          3,
	EvidenceConvention:    4,
}

func compareEvidenceKind(a, b EvidenceKind) int {
	return compareStringOrder(evidenceKindOrder, a, b)
}

var requirementKindOrder = map[RequirementKind]int{
	RequirementRuntime:     0,
	RequirementTool:        1,
	RequirementService:     2,
	RequirementEnvironment: 3,
}

func compareRequirementKind(a, b RequirementKind) int {
	return compareStringOrder(requirementKindOrder, a, b)
}

func compareStringOrder[T ~string](order map[T]int, a, b T) int {
	ai, aKnown := order[a]
	bi, bKnown := order[b]
	if !aKnown {
		ai = len(order)
	}
	if !bKnown {
		bi = len(order)
	}
	if ai != bi {
		return cmp.Compare(ai, bi)
	}
	return cmp.Compare(string(a), string(b))
}
