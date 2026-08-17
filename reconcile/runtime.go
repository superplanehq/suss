package reconcile

import (
	"cmp"
	"strconv"
	"strings"

	"github.com/superplanehq/suss/plan"
)

type runtimeObservation struct {
	dir         string
	requirement plan.Requirement
}

func applyRuntimes(projects []plan.ProjectPlan, observations []runtimeObservation) []plan.ProjectPlan {
	type key struct {
		project int
		name    string
	}
	grouped := make(map[key][]plan.Requirement)
	order := make([]key, 0)
	seen := make(map[key]struct{})

	for _, observation := range observations {
		var index int
		projects, index = ensureProject(projects, observation.dir)
		item := key{project: index, name: observation.requirement.Name}
		if _, ok := seen[item]; !ok {
			seen[item] = struct{}{}
			order = append(order, item)
		}
		grouped[item] = append(grouped[item], observation.requirement)
	}

	for _, item := range order {
		foldRuntime(&projects[item.project], item.name, grouped[item])
	}
	return projects
}

func foldRuntime(project *plan.ProjectPlan, name string, incoming []plan.Requirement) {
	existing := runtimeIndexes(*project, name)
	versions := uniqueVersions(incoming)

	if len(existing) == 0 {
		if len(versions) <= 1 {
			project.Requirements = append(project.Requirements, mergeIncoming(incoming))
			return
		}
		project.Requirements = append(project.Requirements, unversionedRuntime(name, incoming))
		for _, version := range versions {
			addMatrixFact(project, name, version, evidenceForVersion(incoming, version))
		}
		return
	}

	matrix := len(versions) > 1
	for _, requirement := range incoming {
		switch target, kind := matchExistingRuntime(*project, existing, requirement.Version); kind {
		case runtimeEqual:
			mergeRequirementEvidence(&project.Requirements[target], requirement.Evidence)
		case runtimeSatisfies:
			mergeRequirementEvidence(&project.Requirements[target], requirement.Evidence)
		case runtimeMatrixExtra:
			if matrix {
				addMatrixFact(project, name, requirement.Version, requirement.Evidence)
			} else {
				project.Conflicts = append(project.Conflicts, runtimeConflict(project.Requirements[target], requirement))
			}
		}
	}
}

type runtimeMatchKind int

const (
	runtimeEqual runtimeMatchKind = iota
	runtimeSatisfies
	runtimeMatrixExtra
)

func matchExistingRuntime(project plan.ProjectPlan, indexes []int, version string) (int, runtimeMatchKind) {
	for _, index := range indexes {
		if sameVersion(project.Requirements[index].Version, version) {
			return index, runtimeEqual
		}
	}
	for _, index := range indexes {
		if satisfies(project.Requirements[index].Version, version) {
			return index, runtimeSatisfies
		}
	}
	return indexes[0], runtimeMatrixExtra
}

func runtimeIndexes(project plan.ProjectPlan, name string) []int {
	var indexes []int
	for i, requirement := range project.Requirements {
		if requirement.Kind == plan.RequirementRuntime && requirement.Name == name {
			indexes = append(indexes, i)
		}
	}
	return indexes
}

func uniqueVersions(requirements []plan.Requirement) []string {
	var versions []string
	seen := make(map[string]struct{})
	for _, requirement := range requirements {
		if requirement.Version == "" {
			continue
		}
		key := normalizeVersion(requirement.Version)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		versions = append(versions, requirement.Version)
	}
	return versions
}

func evidenceForVersion(requirements []plan.Requirement, version string) []plan.Evidence {
	var evidence []plan.Evidence
	for _, requirement := range requirements {
		if requirement.Version != version {
			continue
		}
		evidence = append(evidence, requirement.Evidence...)
	}
	return evidence
}

func mergeIncoming(incoming []plan.Requirement) plan.Requirement {
	merged := incoming[0]
	for _, requirement := range incoming[1:] {
		merged.Evidence = append(merged.Evidence, requirement.Evidence...)
	}
	return merged
}

func unversionedRuntime(name string, incoming []plan.Requirement) plan.Requirement {
	var evidence []plan.Evidence
	confidence := plan.ConfidenceHigh
	for _, requirement := range incoming {
		evidence = append(evidence, requirement.Evidence...)
		if confidenceRank(requirement.Confidence) < confidenceRank(confidence) {
			confidence = requirement.Confidence
		}
	}
	return plan.Requirement{
		Kind:       plan.RequirementRuntime,
		Name:       name,
		Confidence: confidence,
		Evidence:   evidence,
	}
}

func confidenceRank(confidence plan.Confidence) int {
	switch confidence {
	case plan.ConfidenceHigh:
		return 3
	case plan.ConfidenceMedium:
		return 2
	case plan.ConfidenceLow:
		return 1
	default:
		return 0
	}
}

func mergeRequirementEvidence(requirement *plan.Requirement, evidence []plan.Evidence) {
	requirement.Evidence = append(append([]plan.Evidence{}, requirement.Evidence...), evidence...)
}

func addMatrixFact(project *plan.ProjectPlan, runtime, version string, evidence []plan.Evidence) {
	if version == "" {
		return
	}
	applyAssembledProperty(project, plan.Property{
		Kind:       plan.PropertyFact,
		Name:       "ci.matrix." + runtime,
		Value:      version,
		Confidence: plan.ConfidenceHigh,
		Evidence:   evidence,
	})
}

func runtimeConflict(declared, observed plan.Requirement) plan.Conflict {
	return plan.Conflict{
		Subject: "runtime." + declared.Name + ".version",
		Message: "CI pins a different " + declared.Name + " version than the repository declaration.",
		Assertions: []plan.Candidate{
			{Value: declared.Version, Evidence: declared.Evidence},
			{Value: observed.Version, Evidence: observed.Evidence},
		},
	}
}

func sameVersion(a, b string) bool {
	return normalizeVersion(a) == normalizeVersion(b) && a != "" && b != ""
}

func normalizeVersion(version string) string {
	version = strings.TrimSpace(version)
	version = strings.TrimPrefix(version, "v")
	version = strings.TrimSuffix(version, ".x")
	version = strings.TrimSuffix(version, ".X")
	return version
}

func satisfies(declared, version string) bool {
	if declared == "" || version == "" {
		return false
	}
	if sameVersion(declared, version) {
		return true
	}
	if !isVersionRange(declared) {
		return false
	}

	minimum, ok := rangeMinimum(declared)
	if !ok {
		return true
	}
	return compareVersions(normalizeVersion(version), minimum) >= 0
}

func isVersionRange(version string) bool {
	return strings.ContainsAny(version, "><^~*|") || strings.Contains(version, "||")
}

func rangeMinimum(version string) (string, bool) {
	trimmed := strings.TrimSpace(version)
	switch {
	case strings.HasPrefix(trimmed, ">="):
		return normalizeVersion(strings.TrimSpace(trimmed[2:])), true
	case strings.HasPrefix(trimmed, ">"):
		return normalizeVersion(strings.TrimSpace(trimmed[1:])), true
	default:
		return "", false
	}
}

func compareVersions(a, b string) int {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	n := max(len(as), len(bs))
	for i := 0; i < n; i++ {
		if n := cmp.Compare(versionPart(as, i), versionPart(bs, i)); n != 0 {
			return n
		}
	}
	return 0
}

func versionPart(parts []string, i int) int {
	if i >= len(parts) {
		return 0
	}
	n, err := strconv.Atoi(parts[i])
	if err != nil {
		return 0
	}
	return n
}
