// Package reconcile links CI observations to declared and inferred commands.
// Matching is exact about identity and honest about failure: a CI
// invocation that cannot be linked is kept as an observed command rather than
// guessed into a variant.
//
// Matching rules, evaluated against one observed CI command and the commands
// already assembled for the project that owns its working directory:
//
//  1. Directory. The observed command is assigned to the project whose path is
//     the longest prefix of its working directory. `cd DIR &&` and package
//     manager flags (`yarn --cwd`, `pnpm --dir`, `npm --prefix`) are applied
//     before matching. A command is never copied onto workspace members; that
//     would be fan-out.
//
//  2. Variant. An observation is a CI variant of an existing command when
//     directories match and one of:
//     - both are package-manager script invocations of the same tool and the
//     same script name (extra flags on either side are still that script);
//     - both are dependency-install invocations of the same tool, regardless
//     of frozen-lockfile flags (`npm ci` vs `npm install`, `yarn` vs
//     `yarn install --frozen-lockfile`);
//     - neither is a package-manager invocation, the executables are equal,
//     and the existing command's non-flag tokens are a prefix of the
//     observation (`go test ./...` vs `go test -race ./...`). Flags may
//     appear anywhere; they are ignored for identity.
//     Equal run text is still recorded as a variant: the CI source is distinct
//     evidence, not a duplicate declaration. Observed commands are never
//     matched to other observed commands.
//
//  3. Conflict. Same directory and same script name (or both installs) but a
//     different package manager. The declared command stays; the CI run is
//     recorded as a conflicting assertion, not as a second command.
//
//  4. Unrelated. Anything else remains origin=observed on the assigned
//     project. Install-capable observations and `docker compose up` go to
//     preparation; others to commands. No interpretation is invented beyond
//     the knowledge base.
//
//  5. Ambiguity. If several existing commands satisfy the variant rule, the
//     declared command wins over inferred, then the one with identical extra
//     args, then command ID. The loser is not also linked.
//
// Requirements and facts from CI are merged onto the assigned project by
// (kind, name, version) or (fact name, value). Environment names never include
// values. Workspace-root commands keep repository scope.
//
// Runtime versions from CI are not additional requirements. A matrix is how
// CI tests, not a list of runtimes the developer must install:
//   - a matching pin is merged as evidence on the declaration;
//   - a version that satisfies a declared range is folded into that range;
//   - a version that cannot be evaluated against a declared range is recorded
//     as a ci.matrix.<runtime> fact, not as supporting evidence;
//   - an omitted CI version or a non-numeric alias (lts/*, latest) is
//     unevaluable, not a contradiction;
//   - extra matrix pins beside a declared pin are recorded as ci.matrix.<runtime>
//     facts;
//   - a matrix with no declaration becomes one unversioned runtime plus those
//     facts;
//   - a single CI pin that contradicts a declared pin is a conflict;
//   - an unversioned setup beside a single pin keeps the pin and merges evidence.
package reconcile

import (
	"path"
	"strings"

	"github.com/superplanehq/suss/knowledge"
	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

// PreferDeclared drops inferred convention commands when a declared command
// already covers the same capability. idea.md: conventions fill gaps when
// explicit evidence is absent; `make test` is not replaced by `go test ./...`.
// This is cross-provider reconciliation, not provider logic.
func PreferDeclared(project plan.ProjectPlan) plan.ProjectPlan {
	covered := declaredCapabilities(project)
	if len(covered) == 0 {
		return project
	}
	project.Preparation = dropCoveredInferred(project.Preparation, covered)
	project.Commands = dropCoveredInferred(project.Commands, covered)
	return project
}

func declaredCapabilities(project plan.ProjectPlan) map[plan.Capability]struct{} {
	out := make(map[plan.Capability]struct{})
	for _, command := range append(append([]plan.Command{}, project.Preparation...), project.Commands...) {
		if command.Origin != plan.CommandDeclared {
			continue
		}
		for _, interpretation := range command.Interpretations {
			out[interpretation.Capability] = struct{}{}
		}
	}
	return out
}

func dropCoveredInferred(commands []plan.Command, covered map[plan.Capability]struct{}) []plan.Command {
	if len(commands) == 0 {
		return commands
	}
	out := commands[:0]
	for _, command := range commands {
		if command.Origin == plan.CommandInferred && inferredCovered(command, covered) {
			continue
		}
		out = append(out, command)
	}
	return out
}

func inferredCovered(command plan.Command, covered map[plan.Capability]struct{}) bool {
	if len(command.Interpretations) == 0 {
		return false
	}
	for _, interpretation := range command.Interpretations {
		if _, ok := covered[interpretation.Capability]; ok {
			return true
		}
	}
	return false
}

// Apply merges repository-scoped observations into already-assembled project
// plans. projects is the per-project output of assemble after PreferDeclared;
// repo is the combined result of repository-scoped providers such as GitHub Actions.
func Apply(projects []plan.ProjectPlan, repo provider.Result) []plan.ProjectPlan {
	if len(repo.Findings) == 0 && len(repo.Ambiguities) == 0 && len(repo.Conflicts) == 0 {
		return projects
	}

	var runtimes []runtimeObservation
	for _, finding := range repo.Findings {
		switch item := finding.(type) {
		case plan.CommandFinding:
			projects = applyCommand(projects, item.Command)
		case plan.RequirementFinding:
			if item.Requirement.Kind == plan.RequirementRuntime {
				runtimes = append(runtimes, runtimeObservation{dir: item.ProjectPath, requirement: item.Requirement})
				continue
			}
			projects = applyRequirement(projects, item.ProjectPath, item.Requirement)
		case plan.PropertyFinding:
			projects = applyProperty(projects, item.ProjectPath, item.Property)
		}
	}
	projects = applyRuntimes(projects, runtimes)

	projects = applyNotes(projects, ".", repo.Ambiguities, repo.Conflicts)
	for i := range projects {
		projects[i].ApplyWorkspaceScope()
	}
	return projects
}

func applyCommand(projects []plan.ProjectPlan, observed plan.Command) []plan.ProjectPlan {
	projects, index := ensureProject(projects, observed.Directory)
	project := &projects[index]

	switch outcome := match(observed, existingCommands(*project)); outcome.kind {
	case matchVariant:
		attachVariant(project, outcome.command.ID, observed)
	case matchConflict:
		project.Conflicts = append(project.Conflicts, packageManagerConflict(outcome.command, observed))
	default:
		observed.Scope = projectScope(*project)
		if isPreparation(observed) {
			project.Preparation = append(project.Preparation, observed)
		} else {
			project.Commands = append(project.Commands, observed)
		}
	}
	return projects
}

func applyRequirement(projects []plan.ProjectPlan, dir string, requirement plan.Requirement) []plan.ProjectPlan {
	projects, index := ensureProject(projects, dir)
	project := &projects[index]
	for i, existing := range project.Requirements {
		if !sameRequirement(existing, requirement) {
			continue
		}
		project.Requirements[i].Evidence = mergeEvidence(existing.Evidence, requirement.Evidence)
		if requirement.Kind == plan.RequirementEnvironment {
			project.Requirements[i].IsRequired = orBool(existing.IsRequired, requirement.IsRequired)
			project.Requirements[i].HasDefault = orBool(existing.HasDefault, requirement.HasDefault)
		}
		return projects
	}
	project.Requirements = append(project.Requirements, requirement)
	return projects
}

func applyProperty(projects []plan.ProjectPlan, dir string, property plan.Property) []plan.ProjectPlan {
	projects, index := ensureProject(projects, dir)
	applyAssembledProperty(&projects[index], property)
	return projects
}

func applyAssembledProperty(project *plan.ProjectPlan, property plan.Property) {
	switch property.Kind {
	case plan.PropertyFact:
		for i, fact := range project.Facts {
			if fact.Name == property.Name && fact.Value == property.Value {
				project.Facts[i].Evidence = mergeEvidence(fact.Evidence, property.Evidence)
				return
			}
		}
		project.Facts = append(project.Facts, plan.ProjectFact{
			Name:       property.Name,
			Value:      property.Value,
			Confidence: property.Confidence,
			Evidence:   property.Evidence,
		})
	case plan.PropertyLanguage, plan.PropertyFramework, plan.PropertyPackageManager:
		// Repository providers do not currently emit these; ignore rather than
		// invent a second source of project identity.
	}
}

func applyNotes(projects []plan.ProjectPlan, dir string, ambiguities []plan.Ambiguity, conflicts []plan.Conflict) []plan.ProjectPlan {
	if len(ambiguities) == 0 && len(conflicts) == 0 {
		return projects
	}
	projects, index := ensureProject(projects, dir)
	projects[index].Ambiguities = append(projects[index].Ambiguities, ambiguities...)
	projects[index].Conflicts = append(projects[index].Conflicts, conflicts...)
	return projects
}

func attachVariant(project *plan.ProjectPlan, id plan.CommandID, observed plan.Command) {
	variant := plan.CommandVariant{
		Context:    "ci",
		Run:        deref(observed.Run),
		Directory:  observed.Directory,
		Confidence: plan.ConfidenceHigh,
		Evidence:   observed.Evidence,
	}
	for i := range project.Preparation {
		if project.Preparation[i].ID == id {
			confirmInferred(&project.Preparation[i], observed)
			project.Preparation[i].Variants = upsertVariant(project.Preparation[i].Variants, variant)
			return
		}
	}
	for i := range project.Commands {
		if project.Commands[i].ID == id {
			confirmInferred(&project.Commands[i], observed)
			project.Commands[i].Variants = upsertVariant(project.Commands[i].Variants, variant)
			return
		}
	}
}

func confirmInferred(command *plan.Command, observed plan.Command) {
	if command.Origin != plan.CommandInferred {
		return
	}
	if confidenceRank(command.Confidence) < confidenceRank(plan.ConfidenceHigh) {
		command.Confidence = plan.ConfidenceHigh
	}
	command.Evidence = mergeEvidence(command.Evidence, observed.Evidence)
}

func upsertVariant(variants []plan.CommandVariant, variant plan.CommandVariant) []plan.CommandVariant {
	for i, existing := range variants {
		if existing.Context == variant.Context && existing.Run == variant.Run && existing.Directory == variant.Directory {
			variants[i].Evidence = mergeEvidence(existing.Evidence, variant.Evidence)
			return variants
		}
	}
	return append(variants, variant)
}

func mergeEvidence(existing, incoming []plan.Evidence) []plan.Evidence {
	out := append([]plan.Evidence{}, existing...)
	seen := make(map[string]struct{}, len(out))
	for _, item := range out {
		seen[evidenceKey(item)] = struct{}{}
	}
	for _, item := range incoming {
		key := evidenceKey(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func evidenceKey(item plan.Evidence) string {
	return string(item.Kind) + "\x00" + item.Source + "\x00" + item.Pointer + "\x00" + item.Description
}

func existingCommands(project plan.ProjectPlan) []plan.Command {
	out := make([]plan.Command, 0, len(project.Preparation)+len(project.Commands))
	out = append(out, project.Preparation...)
	out = append(out, project.Commands...)
	return out
}

func ensureProject(projects []plan.ProjectPlan, dir string) ([]plan.ProjectPlan, int) {
	if index := coveringIndex(projects, dir); index >= 0 {
		return projects, index
	}
	if index := coveringIndex(projects, "."); index >= 0 {
		return projects, index
	}
	projects = append(projects, plan.NewProjectPlan("."))
	return projects, len(projects) - 1
}

func coveringIndex(projects []plan.ProjectPlan, dir string) int {
	dir = normalizeDir(dir)
	best := -1
	bestLen := -1
	root := -1
	for i, project := range projects {
		projectPath := normalizeDir(project.Path)
		if projectPath == "." {
			root = i
		}
		if covers(projectPath, dir) && (best < 0 || len(projectPath) > bestLen) {
			best = i
			bestLen = len(projectPath)
		}
	}
	if best >= 0 {
		return best
	}
	if dir == "." {
		return root
	}
	return -1
}

func covers(projectPath, dir string) bool {
	if projectPath == "." {
		return dir == "."
	}
	return dir == projectPath || strings.HasPrefix(dir, projectPath+"/")
}

func normalizeDir(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "." {
		return "."
	}
	value = path.Clean("/" + strings.TrimPrefix(value, "./"))
	value = strings.TrimPrefix(value, "/")
	if value == "" {
		return "."
	}
	return value
}

func projectScope(project plan.ProjectPlan) plan.CommandScope {
	if project.HasWorkspaceOrchestrator() {
		return plan.ScopeRepository
	}
	return plan.ScopeProject
}

func sameRequirement(a, b plan.Requirement) bool {
	if a.Kind != b.Kind || a.Name != b.Name {
		return false
	}
	if a.Kind == plan.RequirementEnvironment {
		return true
	}
	return a.Version == b.Version
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

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// isPreparation uses the same rule as assemble.shouldPrepare: install-capable
// commands and docker compose up are preparation, regardless of origin.
func isPreparation(command plan.Command) bool {
	return isInstall(command) || isComposeUp(command)
}

func isInstall(command plan.Command) bool {
	for _, interpretation := range command.Interpretations {
		if interpretation.Capability == plan.CapabilityDependenciesInstall {
			return true
		}
	}
	return false
}

func isComposeUp(command plan.Command) bool {
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
