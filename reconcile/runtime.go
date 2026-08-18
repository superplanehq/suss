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
		case runtimeEqual, runtimeSatisfies:
			mergeRequirementEvidence(&project.Requirements[target], requirement.Evidence)
		case runtimeUnevaluable:
			addMatrixFact(project, name, requirement.Version, requirement.Evidence)
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
	runtimeUnevaluable
	runtimeMatrixExtra
)

func matchExistingRuntime(project plan.ProjectPlan, indexes []int, version string) (int, runtimeMatchKind) {
	for _, index := range indexes {
		if sameVersion(project.Requirements[index].Version, version) {
			return index, runtimeEqual
		}
	}
	unevaluable := -1
	for _, index := range indexes {
		ok, known := versionSatisfies(project.Requirements[index].Name, project.Requirements[index].Version, version)
		if !known {
			if unevaluable < 0 {
				unevaluable = index
			}
			continue
		}
		if ok {
			return index, runtimeSatisfies
		}
	}
	if unevaluable >= 0 {
		return unevaluable, runtimeUnevaluable
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
	for _, requirement := range incoming {
		if requirement.Version != "" {
			merged = requirement
			break
		}
	}
	var evidence []plan.Evidence
	for _, requirement := range incoming {
		evidence = mergeEvidence(evidence, requirement.Evidence)
	}
	merged.Evidence = evidence
	return merged
}

func unversionedRuntime(name string, incoming []plan.Requirement) plan.Requirement {
	var evidence []plan.Evidence
	confidence := plan.ConfidenceHigh
	for _, requirement := range incoming {
		evidence = mergeEvidence(evidence, requirement.Evidence)
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
	requirement.Evidence = mergeEvidence(requirement.Evidence, evidence)
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

func inequalitySatisfies(raw, version string) (ok, known bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false, false
	}
	if strings.ContainsAny(raw, "xX*") {
		matched, known := wildcardSatisfies(raw, version)
		if !known {
			return false, false
		}
		return !matched, true
	}
	bound := normalizeVersion(raw)
	if bound == "" || !comparableVersion(bound) {
		return false, false
	}
	return compareVersions(version, bound) != 0, true
}

func normalizeVersion(version string) string {
	version = strings.TrimSpace(version)
	version = strings.TrimPrefix(version, "v")
	version = strings.TrimSuffix(version, ".x")
	version = strings.TrimSuffix(version, ".X")
	return version
}

func versionSatisfies(runtime, declared, version string) (ok, known bool) {
	declared = strings.TrimSpace(declared)
	version = normalizeVersion(version)
	if declared == "" || version == "" || !comparableVersion(version) {
		return false, false
	}
	if sameVersion(declared, version) {
		return true, true
	}

	for _, group := range strings.Split(strings.ReplaceAll(declared, "||", "|"), "|") {
		satisfied, groupKnown := andConstraints(runtime, strings.TrimSpace(group), version)
		if !groupKnown {
			return false, false
		}
		if satisfied {
			return true, true
		}
	}
	return false, true
}

func andConstraints(runtime, group, version string) (ok, known bool) {
	if left, right, hyphen := splitHyphenRange(group); hyphen {
		return hyphenSatisfies(runtime, left, right, version)
	}
	tokens := splitConstraints(group)
	if len(tokens) == 0 {
		return false, false
	}
	for _, token := range tokens {
		matched, tokenKnown := constraintSatisfies(runtime, token, version)
		if !tokenKnown {
			return false, false
		}
		if !matched {
			return false, true
		}
	}
	return true, true
}

func splitHyphenRange(group string) (left, right string, ok bool) {
	left, right, ok = strings.Cut(group, " - ")
	if !ok {
		return "", "", false
	}
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" || right == "" {
		return "", "", false
	}
	return left, right, true
}

func hyphenSatisfies(runtime, left, right, version string) (ok, known bool) {
	if runtime != "php" {
		return false, false
	}
	lower := normalizeVersion(left)
	if lower == "" || !comparableVersion(lower) {
		return false, false
	}
	if compareVersions(version, lower) < 0 {
		return false, true
	}
	return composerHyphenUpperBound(right, version)
}

func composerHyphenUpperBound(right, version string) (ok, known bool) {
	right = strings.TrimSpace(strings.TrimPrefix(right, "v"))
	if right == "" {
		return false, false
	}
	parts := strings.Split(right, ".")
	base := normalizeVersion(right)
	if base == "" || !comparableVersion(base) {
		return false, false
	}
	if len(parts) >= 3 {
		return compareVersions(version, base) <= 0, true
	}
	major := versionPart(parts, 0)
	if len(parts) == 1 {
		return compareVersions(version, formatVersion(major+1, 0, 0)) < 0, true
	}
	minor := versionPart(parts, 1)
	return compareVersions(version, formatVersion(major, minor+1, 0)) < 0, true
}

func splitConstraints(group string) []string {
	var tokens []string
	// Composer treats comma as AND (`>=8.1,<8.4`); npm-style ranges use spaces.
	fields := strings.Fields(strings.ReplaceAll(group, ",", " "))
	for i := 0; i < len(fields); i++ {
		field := fields[i]
		if field == ">=" || field == "<=" || field == ">" || field == "<" || field == "!=" || field == "<>" || field == "^" || field == "~" || field == "~>" {
			if i+1 < len(fields) {
				tokens = append(tokens, field+fields[i+1])
				i++
			}
			continue
		}
		tokens = append(tokens, field)
	}
	return tokens
}

func constraintSatisfies(runtime, token, version string) (ok, known bool) {
	switch {
	case strings.HasPrefix(token, ">="):
		bound := normalizeVersion(strings.TrimSpace(token[2:]))
		if bound == "" {
			return false, false
		}
		return compareVersions(version, bound) >= 0, true
	case strings.HasPrefix(token, "<="):
		bound := normalizeVersion(strings.TrimSpace(token[2:]))
		if bound == "" {
			return false, false
		}
		return compareVersions(version, bound) <= 0, true
	case strings.HasPrefix(token, "!="), strings.HasPrefix(token, "<>"):
		return inequalitySatisfies(strings.TrimSpace(token[2:]), version)
	case strings.HasPrefix(token, ">"):
		bound := normalizeVersion(strings.TrimSpace(token[1:]))
		if bound == "" {
			return false, false
		}
		return compareVersions(version, bound) > 0, true
	case strings.HasPrefix(token, "<"):
		bound := normalizeVersion(strings.TrimSpace(token[1:]))
		if bound == "" {
			return false, false
		}
		return compareVersions(version, bound) < 0, true
	case strings.HasPrefix(token, "^"):
		return caretSatisfies(strings.TrimSpace(token[1:]), version)
	case strings.HasPrefix(token, "~>"):
		return pessimisticSatisfies(strings.TrimSpace(token[2:]), version)
	case strings.HasPrefix(token, "~"):
		return tildeSatisfies(strings.TrimSpace(token[1:]), version, runtime == "php")
	case strings.ContainsAny(token, "xX*"):
		return wildcardSatisfies(token, version)
	default:
		if strings.ContainsAny(token, "><^~|!") || strings.Contains(token, "-") {
			return false, false
		}
		return sameVersion(token, version), true
	}
}

// pessimisticSatisfies implements Elixir/Hex's ~> requirement: a two-part
// requirement permits the next major, while three or more parts permit the
// next minor.
func pessimisticSatisfies(raw, version string) (ok, known bool) {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "v")
	base := normalizeVersion(raw)
	if base == "" || !comparableVersion(base) {
		return false, false
	}
	if compareVersions(version, base) < 0 {
		return false, true
	}
	parts := strings.Split(raw, ".")
	major := versionPart(parts, 0)
	if len(parts) <= 2 {
		return compareVersions(version, formatVersion(major+1, 0, 0)) < 0, true
	}
	minor := versionPart(parts, 1)
	return compareVersions(version, formatVersion(major, minor+1, 0)) < 0, true
}

func caretSatisfies(base, version string) (ok, known bool) {
	base = normalizeVersion(base)
	if base == "" {
		return false, false
	}
	if compareVersions(version, base) < 0 {
		return false, true
	}
	parts := strings.Split(base, ".")
	major := versionPart(parts, 0)
	minor := versionPart(parts, 1)
	patch := versionPart(parts, 2)
	switch {
	case major > 0:
		return compareVersions(version, formatVersion(major+1, 0, 0)) < 0, true
	case minor > 0:
		return compareVersions(version, formatVersion(0, minor+1, 0)) < 0, true
	default:
		return compareVersions(version, formatVersion(0, 0, patch+1)) < 0, true
	}
}

func tildeSatisfies(raw, version string, composer bool) (ok, known bool) {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "v")
	base := normalizeVersion(raw)
	if base == "" {
		return false, false
	}
	if compareVersions(version, base) < 0 {
		return false, true
	}
	parts := strings.Split(raw, ".")
	major := versionPart(parts, 0)
	// Composer ~8.1 is >=8.1 <9.0.0. npm ~8.1 is >=8.1.0 <8.2.0.
	if len(parts) == 1 || (composer && len(parts) == 2) {
		return compareVersions(version, formatVersion(major+1, 0, 0)) < 0, true
	}
	minor := versionPart(parts, 1)
	return compareVersions(version, formatVersion(major, minor+1, 0)) < 0, true
}

func wildcardSatisfies(token, version string) (ok, known bool) {
	token = strings.TrimPrefix(strings.TrimSpace(token), "v")
	if token == "*" || token == "x" || token == "X" {
		return true, true
	}
	parts := strings.Split(token, ".")
	if len(parts) == 0 {
		return false, false
	}
	if parts[0] == "*" || parts[0] == "x" || parts[0] == "X" {
		return true, true
	}
	major := versionPart(parts, 0)
	if len(parts) == 1 || isWildcardPart(parts[1]) {
		return compareVersions(version, formatVersion(major, 0, 0)) >= 0 && compareVersions(version, formatVersion(major+1, 0, 0)) < 0, true
	}
	minor := versionPart(parts, 1)
	return compareVersions(version, formatVersion(major, minor, 0)) >= 0 && compareVersions(version, formatVersion(major, minor+1, 0)) < 0, true
}

func isWildcardPart(part string) bool {
	return part == "*" || part == "x" || part == "X"
}

func formatVersion(major, minor, patch int) string {
	return strconv.Itoa(major) + "." + strconv.Itoa(minor) + "." + strconv.Itoa(patch)
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

func comparableVersion(version string) bool {
	if version == "" {
		return false
	}
	for _, part := range strings.Split(version, ".") {
		if _, err := strconv.Atoi(part); err != nil {
			return false
		}
	}
	return true
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
