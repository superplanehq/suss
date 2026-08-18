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
			Message:    competingManagerMessage(names),
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

func competingManagerMessage(names []string) string {
	return fmt.Sprintf("Competing lockfiles support %s and no stronger declaration selects one.", strings.Join(names, " and "))
}

func managerCandidates(signals []managerSignal) []plan.Candidate {
	candidates := make([]plan.Candidate, 0, len(signals))
	for _, signal := range signals {
		candidates = append(candidates, plan.Candidate{Value: signal.name, Evidence: signal.evidence})
	}
	return candidates
}
