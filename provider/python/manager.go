package python

import (
	"fmt"
	"slices"
	"strings"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

type managerChoice struct {
	selected    string
	install     string
	findings    []plan.Finding
	ambiguities []plan.Ambiguity
	signals     []managerSignal
}

type managerSignal struct {
	name     string
	lockfile string
	evidence []plan.Evidence
}

var lockfiles = []lockfile{
	{Manager: "uv", File: "uv.lock"},
	{Manager: "poetry", File: "poetry.lock"},
	{Manager: "pdm", File: "pdm.lock"},
	{Manager: "pipenv", File: "Pipfile.lock"},
}

func choosePackageManager(ctx provider.Context, project pythonProject) managerChoice {
	signals := collectManagerSignals(ctx, project)

	choice := managerChoice{signals: signals}
	for _, signal := range signals {
		choice.findings = append(choice.findings, plan.PropertyFinding{
			ProjectPath: ctx.ProjectPath,
			Detector:    providerName,
			Property: plan.Property{
				Kind:       plan.PropertyPackageManager,
				Name:       signal.name,
				Confidence: plan.ConfidenceHigh,
				Evidence:   signal.evidence,
			},
		})
	}

	names := uniqueManagerNames(signals)
	switch len(names) {
	case 0:
		if isInstallable(ctx, project) {
			choice.selected = "pip"
			choice.install = pipInstallRun(ctx)
			choice.findings = append(choice.findings, inferredPipFinding(ctx, project))
		}
	case 1:
		choice.selected = names[0]
		choice.install = installRun(ctx, names[0])
	default:
		choice.ambiguities = append(choice.ambiguities, plan.Ambiguity{
			Subject:    "tool.package-manager",
			Message:    competingManagerMessage(signals, names),
			Candidates: managerCandidates(signals),
		})
	}
	return choice
}

func collectManagerSignals(ctx provider.Context, project pythonProject) []managerSignal {
	var signals []managerSignal
	for _, lock := range lockfiles {
		if !fileExists(ctx.ProjectDir(), lock.File) {
			continue
		}
		signals = append(signals, managerSignal{
			name:     lock.Manager,
			lockfile: lock.File,
			evidence: []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: ctx.SourcePath(lock.File)}},
		})
	}
	if fileExists(ctx.ProjectDir(), "Pipfile") && !hasManager(signals, "pipenv") {
		signals = append(signals, managerSignal{
			name: "pipenv",
			evidence: []plan.Evidence{{
				Kind:   plan.EvidenceDeclaration,
				Source: ctx.SourcePath("Pipfile"),
			}},
		})
	}

	for _, name := range []string{"poetry", "uv", "pdm"} {
		if _, ok := project.ManagerTables[name]; !ok || hasManager(signals, name) {
			continue
		}
		signals = append(signals, managerSignal{
			name: name,
			evidence: []plan.Evidence{{
				Kind:    plan.EvidenceDeclaration,
				Source:  ctx.SourcePath(project.Manifest),
				Pointer: "/tool/" + name,
			}},
		})
	}

	if fileExists(ctx.ProjectDir(), "requirements.txt") && len(signals) == 0 {
		signals = append(signals, managerSignal{
			name: "pip",
			evidence: []plan.Evidence{{
				Kind:   plan.EvidenceDeclaration,
				Source: ctx.SourcePath("requirements.txt"),
			}},
		})
	}
	return signals
}

func inferredPipFinding(ctx provider.Context, project pythonProject) plan.Finding {
	return plan.PropertyFinding{
		ProjectPath: ctx.ProjectPath,
		Detector:    providerName,
		Property: plan.Property{
			Kind:       plan.PropertyPackageManager,
			Name:       "pip",
			Confidence: plan.ConfidenceMedium,
			Evidence: []plan.Evidence{{
				Kind:        plan.EvidenceConvention,
				Source:      "python-ecosystem",
				Pointer:     "package-manager",
				Description: "Python projects without a lockfile conventionally use pip.",
			}, {
				Kind:   plan.EvidenceDeclaration,
				Source: ctx.SourcePath(project.Manifest),
			}},
		},
	}
}

func installRun(ctx provider.Context, manager string) string {
	switch manager {
	case "uv":
		return "uv sync"
	case "poetry":
		return "poetry install"
	case "pdm":
		return "pdm install"
	case "pipenv":
		return "pipenv install"
	case "pip":
		return pipInstallRun(ctx)
	default:
		return ""
	}
}

func applyInstallSelectors(manager, run string, extras, groups []string) string {
	if run == "" {
		return run
	}
	slices.Sort(extras)
	slices.Sort(groups)
	switch manager {
	case "pip":
		if len(extras) > 0 {
			spec := " -e " + pipExtrasSpec(extras)
			if run == "pip install -e ." {
				run = "pip install -e " + pipExtrasSpec(extras)
			} else {
				run += spec
			}
		}
		for _, group := range groups {
			run += " --group " + group
		}
		return run
	case "uv":
		for _, extra := range extras {
			run += " --extra " + extra
		}
		for _, group := range groups {
			run += " --group " + group
		}
		return run
	case "poetry":
		if len(extras) > 0 {
			run += " --extras " + strings.Join(extras, ",")
		}
		for _, group := range groups {
			run += " --with " + group
		}
		return run
	case "pdm":
		selectors := append(append([]string{}, extras...), groups...)
		slices.Sort(selectors)
		selectors = slices.Compact(selectors)
		for _, name := range selectors {
			run += " -G " + name
		}
		return run
	case "pipenv":
		if len(groups) > 0 || len(extras) > 0 {
			return "pipenv install --dev"
		}
		return run
	default:
		return run
	}
}

func installSelectorsFor(project pythonProject, manager string, names []string) (extras, groups []string) {
	extraSet := map[string]struct{}{}
	groupSet := map[string]struct{}{}
	for _, name := range names {
		dep, ok := project.Dependencies[name]
		if !ok || depInstalledByDefault(project, manager, dep) {
			continue
		}
		kind, selector := preferredSelector(dep)
		switch kind {
		case depKindExtra:
			extraSet[selector] = struct{}{}
		case depKindGroup:
			if !groupInstalledByDefault(project, manager, selector) {
				groupSet[selector] = struct{}{}
			}
		}
	}
	for extra := range extraSet {
		extras = append(extras, extra)
	}
	for group := range groupSet {
		groups = append(groups, group)
	}
	slices.Sort(extras)
	slices.Sort(groups)
	return extras, groups
}

func pytestSignalNames(project pythonProject) []string {
	if _, ok := project.Dependencies["pytest"]; ok {
		return []string{"pytest"}
	}
	if _, ok := project.Dependencies["pytest-django"]; ok {
		return []string{"pytest-django"}
	}
	if dep, ok := prefixedDependency(project, "pytest"); ok {
		return []string{dep.Name}
	}
	return nil
}

func depInstallable(project pythonProject, manager string, dep depDeclaration) bool {
	if depInstalledByDefault(project, manager, dep) {
		return true
	}
	kind, name := preferredSelector(dep)
	return kind != "" && name != ""
}

func depInstalledByDefault(project pythonProject, manager string, dep depDeclaration) bool {
	if len(dep.Origins) == 0 {
		return true
	}
	for _, origin := range dep.Origins {
		if origin.Kind == depKindMain {
			return true
		}
		if origin.Kind == depKindGroup && groupInstalledByDefault(project, manager, origin.Group) {
			return true
		}
	}
	return false
}

func preferredSelector(dep depDeclaration) (kind, name string) {
	var extras, groups []string
	for _, origin := range dep.Origins {
		if origin.Group == "" {
			continue
		}
		switch origin.Kind {
		case depKindExtra:
			extras = append(extras, origin.Group)
		case depKindGroup:
			groups = append(groups, origin.Group)
		}
	}
	slices.Sort(extras)
	slices.Sort(groups)
	if len(extras) > 0 {
		return depKindExtra, extras[0]
	}
	if len(groups) > 0 {
		return depKindGroup, groups[0]
	}
	return "", ""
}

func groupInstalledByDefault(project pythonProject, manager, group string) bool {
	if group == "" {
		return false
	}
	switch manager {
	case "uv":
		if project.HasUVDefaultGroups {
			return slices.Contains(project.UVDefaultGroups, group)
		}
		return group == "dev"
	case "poetry":
		_, optional := project.OptionalPoetryGroups[group]
		return !optional
	case "pdm":
		return true
	default:
		return false
	}
}

func pipExtrasSpec(extras []string) string {
	return "'.[" + strings.Join(extras, ",") + "]'"
}

func pipInstallRun(ctx provider.Context) string {
	if fileExists(ctx.ProjectDir(), "requirements.txt") {
		return "pip install -r requirements.txt"
	}
	return "pip install -e ."
}

func isInstallable(ctx provider.Context, project pythonProject) bool {
	if project.HasProjectTable || project.HasPackageTable {
		return true
	}
	if project.Manifest == "Pipfile" {
		return true
	}
	return fileExists(ctx.ProjectDir(), "requirements.txt")
}

func hasManager(signals []managerSignal, name string) bool {
	for _, signal := range signals {
		if signal.name == name {
			return true
		}
	}
	return false
}

func uniqueManagerNames(signals []managerSignal) []string {
	names := make([]string, 0, len(signals))
	seen := make(map[string]struct{}, len(signals))
	for _, signal := range signals {
		if _, ok := seen[signal.name]; ok {
			continue
		}
		seen[signal.name] = struct{}{}
		names = append(names, signal.name)
	}
	slices.Sort(names)
	return names
}

func competingManagerMessage(signals []managerSignal, names []string) string {
	joined := strings.Join(names, " and ")
	if competingSignalsAllHaveLockfiles(signals, names) {
		return fmt.Sprintf("Competing lockfiles support %s and no stronger declaration selects one.", joined)
	}
	return fmt.Sprintf("Competing package-manager signals support %s and no stronger declaration selects one.", joined)
}

func competingSignalsAllHaveLockfiles(signals []managerSignal, names []string) bool {
	locked := make(map[string]struct{}, len(names))
	for _, signal := range signals {
		if signal.lockfile != "" {
			locked[signal.name] = struct{}{}
		}
	}
	for _, name := range names {
		if _, ok := locked[name]; !ok {
			return false
		}
	}
	return len(names) > 0
}

func managerCandidates(signals []managerSignal) []plan.Candidate {
	candidates := make([]plan.Candidate, 0, len(signals))
	for _, signal := range signals {
		candidates = append(candidates, plan.Candidate{Value: signal.name, Evidence: signal.evidence})
	}
	return candidates
}
