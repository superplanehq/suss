package node

import (
	"cmp"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

type packageManagerChoice struct {
	selected    string
	version     string
	hasLockfile bool
	yarnBerry   bool
	findings    []plan.Finding
	ambiguities []plan.Ambiguity
	conflicts   []plan.Conflict
}

type managerSignal struct {
	name       string
	version    string
	confidence plan.Confidence
	declared   bool
	lockfile   string
	evidence   []plan.Evidence
}

var lockfiles = []struct {
	file    string
	manager string
}{
	{file: "package-lock.json", manager: "npm"},
	{file: "npm-shrinkwrap.json", manager: "npm"},
	{file: "pnpm-lock.yaml", manager: "pnpm"},
	{file: "yarn.lock", manager: "yarn"},
	{file: "bun.lock", manager: "bun"},
	{file: "bun.lockb", manager: "bun"},
}

func choosePackageManager(ctx provider.Context, manifest packageManifest) (packageManagerChoice, error) {
	signals, err := collectManagerSignals(ctx, manifest)
	if err != nil {
		return packageManagerChoice{}, err
	}

	tools := make([]plan.DetectedTool, 0, len(signals))
	for _, signal := range signals {
		tools = append(tools, plan.DetectedTool{
			Name:       signal.name,
			Version:    signal.version,
			Confidence: signal.confidence,
			Evidence:   signal.evidence,
		})
	}

	choice := packageManagerChoice{}
	for _, tool := range tools {
		choice.findings = append(choice.findings, plan.PropertyFinding{
			ProjectPath: ctx.ProjectPath,
			Detector:    providerName,
			Property: plan.Property{
				Kind:       plan.PropertyPackageManager,
				Name:       tool.Name,
				Version:    tool.Version,
				Confidence: tool.Confidence,
				Evidence:   tool.Evidence,
			},
		})
	}

	declared := declaredManager(signals)
	if declared != nil {
		choice.selected = declared.name
		choice.version = declared.version
		choice.hasLockfile = lockfileFor(signals, declared.name) != ""
		choice.yarnBerry = isYarnBerry(ctx, declared.name)
		if conflicting := otherLockfileManagers(signals, declared.name); len(conflicting) > 0 {
			choice.conflicts = append(choice.conflicts, packageManagerConflict(declared, signals))
		}
		return choice, nil
	}

	names := uniqueManagerNames(signals)
	switch len(names) {
	case 0:
		choice.selected = "npm"
		choice.findings = append(choice.findings, inferredNpmFinding(ctx))
	case 1:
		signal := signals[0]
		choice.selected = signal.name
		choice.version = signal.version
		choice.hasLockfile = signal.lockfile != ""
		choice.yarnBerry = isYarnBerry(ctx, signal.name)
	default:
		choice.ambiguities = append(choice.ambiguities, plan.Ambiguity{
			Subject:    "tool.package-manager",
			Message:    competingManagerMessage(names),
			Candidates: managerCandidates(signals),
		})
	}
	return choice, nil
}

func collectManagerSignals(ctx provider.Context, manifest packageManifest) ([]managerSignal, error) {
	signals, err := collectManagerSignalsAt(ctx, manifest)
	if err != nil || len(signals) > 0 {
		return signals, err
	}

	parent := parentContext(ctx)
	for parent != nil {
		parentManifest, ok, err := readManifest(*parent)
		if err != nil {
			return nil, err
		}
		if !ok {
			parentManifest = packageManifest{}
		}
		signals, err = collectManagerSignalsAt(*parent, parentManifest)
		if err != nil || len(signals) > 0 {
			return signals, err
		}
		parent = parentContext(*parent)
	}
	return nil, nil
}

func collectManagerSignalsAt(ctx provider.Context, manifest packageManifest) ([]managerSignal, error) {
	byName := make(map[string]*managerSignal)

	if manifest.PackageManager != "" {
		name, version, err := parsePackageManagerField(manifest.PackageManager)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", ctx.SourcePath("package.json"), err)
		}
		byName[name] = &managerSignal{
			name:       name,
			version:    version,
			confidence: plan.ConfidenceHigh,
			declared:   true,
			evidence: []plan.Evidence{{
				Kind:    plan.EvidenceDeclaration,
				Source:  ctx.SourcePath("package.json"),
				Pointer: "/packageManager",
			}},
		}
	}

	for _, lockfile := range lockfiles {
		if !fileExists(ctx.ProjectDir(), lockfile.file) {
			continue
		}
		signal := byName[lockfile.manager]
		if signal == nil {
			signal = &managerSignal{
				name:       lockfile.manager,
				confidence: plan.ConfidenceHigh,
			}
			byName[lockfile.manager] = signal
		}
		signal.lockfile = lockfile.file
		signal.evidence = append(signal.evidence, plan.Evidence{
			Kind:   plan.EvidenceFile,
			Source: ctx.SourcePath(lockfile.file),
		})
	}

	npmrc, err := npmrcNpmEvidence(ctx)
	if err != nil {
		return nil, err
	}
	if npmrc != nil {
		if signal := byName["npm"]; signal != nil {
			signal.evidence = append(signal.evidence, *npmrc)
		} else if len(byName) == 0 {
			byName["npm"] = &managerSignal{
				name:       "npm",
				confidence: plan.ConfidenceHigh,
				evidence:   []plan.Evidence{*npmrc},
			}
		}
	}

	signals := make([]managerSignal, 0, len(byName))
	for _, signal := range byName {
		signals = append(signals, *signal)
	}
	slices.SortFunc(signals, func(a, b managerSignal) int {
		return cmp.Compare(a.name, b.name)
	})
	return signals, nil
}

func parsePackageManagerField(field string) (string, string, error) {
	field = strings.TrimSpace(field)
	name, version, _ := strings.Cut(field, "@")
	version, _, _ = strings.Cut(version, "+")
	switch name {
	case "npm", "pnpm", "yarn", "bun":
		return name, version, nil
	default:
		return "", "", fmt.Errorf("unsupported packageManager %q", field)
	}
}

func npmrcNpmEvidence(ctx provider.Context) (*plan.Evidence, error) {
	contents, ok, err := readFile(ctx.ProjectDir(), ".npmrc")
	if err != nil || !ok {
		return nil, err
	}
	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if strings.TrimSpace(key) == "package-lock" {
			description := "The file sets npm's package-lock option."
			if ok {
				description = fmt.Sprintf("The file sets npm's package-lock option to %s.", strings.TrimSpace(value))
			}
			return &plan.Evidence{
				Kind:        plan.EvidenceFile,
				Source:      ctx.SourcePath(".npmrc"),
				Description: description,
			}, nil
		}
	}
	return nil, nil
}

func inferredNpmFinding(ctx provider.Context) plan.Finding {
	return plan.PropertyFinding{
		ProjectPath: ctx.ProjectPath,
		Detector:    providerName,
		Property: plan.Property{
			Kind:       plan.PropertyPackageManager,
			Name:       "npm",
			Confidence: plan.ConfidenceMedium,
			Evidence: []plan.Evidence{{
				Kind:        plan.EvidenceConvention,
				Source:      "node-ecosystem",
				Pointer:     "default-npm",
				Description: "No lockfile or packageManager field selects another tool; npm is the default Node package manager.",
			}, {
				Kind:   plan.EvidenceDeclaration,
				Source: ctx.SourcePath("package.json"),
			}},
		},
	}
}

func declaredManager(signals []managerSignal) *managerSignal {
	for i := range signals {
		if signals[i].declared {
			return &signals[i]
		}
	}
	return nil
}

func lockfileFor(signals []managerSignal, name string) string {
	for _, signal := range signals {
		if signal.name == name {
			return signal.lockfile
		}
	}
	return ""
}

func otherLockfileManagers(signals []managerSignal, selected string) []string {
	var names []string
	for _, signal := range signals {
		if signal.name != selected && signal.lockfile != "" {
			names = append(names, signal.name)
		}
	}
	return names
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
	return names
}

func competingManagerMessage(names []string) string {
	sorted := append([]string{}, names...)
	slices.Sort(sorted)
	return fmt.Sprintf("Competing lockfiles support %s and no stronger declaration selects one.", strings.Join(sorted, " and "))
}

func managerCandidates(signals []managerSignal) []plan.Candidate {
	candidates := make([]plan.Candidate, 0, len(signals))
	for _, signal := range signals {
		candidates = append(candidates, plan.Candidate{
			Value:    signal.name,
			Evidence: signal.evidence,
		})
	}
	return candidates
}

func packageManagerConflict(selected *managerSignal, signals []managerSignal) plan.Conflict {
	assertions := make([]plan.Candidate, 0, len(signals))
	for _, signal := range signals {
		assertions = append(assertions, plan.Candidate{
			Value:    signal.name,
			Evidence: signal.evidence,
		})
	}
	return plan.Conflict{
		Subject:    "tool.package-manager",
		Message:    "The packageManager declaration selects " + selected.name + ", but another lockfile is also present.",
		Assertions: assertions,
		Resolution: &plan.Resolution{
			SelectedValue: selected.name,
			Reason:        "The explicit packageManager field outweighs a lockfile for a different tool.",
			Confidence:    plan.ConfidenceHigh,
			Evidence:      selected.evidence,
		},
	}
}

func isYarnBerry(ctx provider.Context, manager string) bool {
	if manager != "yarn" {
		return false
	}
	contents, ok, err := readFile(ctx.ProjectDir(), "yarn.lock")
	if err != nil || !ok {
		return false
	}
	return strings.Contains(contents, "__metadata:")
}

type installResult struct {
	command     *plan.Command
	ambiguities []plan.Ambiguity
}

func installCommand(ctx provider.Context, choice packageManagerChoice) (installResult, error) {
	if choice.selected == "" {
		return installResult{ambiguities: []plan.Ambiguity{installAmbiguity(ctx, choice)}}, nil
	}

	run := installRun(choice.selected, choice.hasLockfile, choice.yarnBerry)
	convention := installConvention(choice.selected, choice.hasLockfile, choice.yarnBerry)
	id, err := plan.NewCommandID(installIdentity(ctx, choice))
	if err != nil {
		return installResult{}, err
	}

	evidence := []plan.Evidence{{
		Kind:    plan.EvidenceConvention,
		Source:  "node-ecosystem",
		Pointer: convention,
	}}
	for _, finding := range choice.findings {
		property, ok := selectedPackageManager(finding, choice.selected)
		if !ok {
			continue
		}
		evidence = append(append([]plan.Evidence{}, property.Property.Evidence...), evidence...)
	}

	command := plan.Command{
		ID:         id,
		Name:       "install dependencies",
		Run:        stringPtr(run),
		Directory:  ctx.ProjectPath,
		Scope:      plan.ScopeProject,
		Origin:     plan.CommandInferred,
		Confidence: installConfidence(choice),
		Evidence:   evidence,
		Interpretations: []plan.Interpretation{{
			Capability: plan.CapabilityDependenciesInstall,
			Confidence: plan.ConfidenceHigh,
			Evidence: []plan.Evidence{{
				Kind:    plan.EvidenceConvention,
				Source:  "node-ecosystem",
				Pointer: convention,
			}},
		}},
		Variants: []plan.CommandVariant{},
	}
	return installResult{command: &command}, nil
}

func installIdentity(ctx provider.Context, choice packageManagerChoice) plan.CommandIdentity {
	identity := plan.CommandIdentity{
		ProjectPath: ctx.ProjectPath,
		Provider:    providerName,
		Source:      ctx.SourcePath("package.json"),
		Pointer:     "/#install",
	}
	for _, finding := range choice.findings {
		property, ok := selectedPackageManager(finding, choice.selected)
		if !ok {
			continue
		}
		for _, evidence := range property.Property.Evidence {
			if evidence.Pointer == "/packageManager" {
				identity.Pointer = "/packageManager#install"
				return identity
			}
		}
	}
	return identity
}

func installRun(manager string, hasLockfile, yarnBerry bool) string {
	switch manager {
	case "npm":
		if hasLockfile {
			return "npm ci"
		}
		return "npm install"
	case "pnpm":
		if hasLockfile {
			return "pnpm install --frozen-lockfile"
		}
		return "pnpm install"
	case "yarn":
		if !hasLockfile {
			return "yarn install"
		}
		if yarnBerry {
			return "yarn install --immutable"
		}
		return "yarn install --frozen-lockfile"
	case "bun":
		if hasLockfile {
			return "bun install --frozen-lockfile"
		}
		return "bun install"
	default:
		return manager + " install"
	}
}

func installConvention(manager string, hasLockfile, yarnBerry bool) string {
	switch {
	case manager == "npm" && hasLockfile:
		return "npm-ci"
	case manager == "npm":
		return "npm-install"
	case manager == "pnpm" && hasLockfile:
		return "pnpm-frozen-install"
	case manager == "pnpm":
		return "pnpm-install"
	case manager == "yarn" && yarnBerry && hasLockfile:
		return "yarn-immutable"
	case manager == "yarn" && hasLockfile:
		return "yarn-frozen"
	case manager == "yarn":
		return "yarn-install"
	case manager == "bun" && hasLockfile:
		return "bun-frozen-install"
	default:
		return "bun-install"
	}
}

func installConfidence(choice packageManagerChoice) plan.Confidence {
	if choice.hasLockfile {
		return plan.ConfidenceHigh
	}
	for _, finding := range choice.findings {
		property, ok := selectedPackageManager(finding, choice.selected)
		if !ok {
			continue
		}
		return property.Property.Confidence
	}
	return plan.ConfidenceMedium
}

func selectedPackageManager(finding plan.Finding, selected string) (plan.PropertyFinding, bool) {
	property, ok := finding.(plan.PropertyFinding)
	if !ok || property.Property.Kind != plan.PropertyPackageManager || property.Property.Name != selected {
		return plan.PropertyFinding{}, false
	}
	return property, true
}

func installAmbiguity(ctx provider.Context, choice packageManagerChoice) plan.Ambiguity {
	candidates := make([]plan.Candidate, 0)
	for _, finding := range choice.findings {
		property, ok := finding.(plan.PropertyFinding)
		if !ok || property.Property.Kind != plan.PropertyPackageManager {
			continue
		}
		hasLockfile := evidenceIncludesLockfile(property.Property.Evidence)
		run := installRun(property.Property.Name, hasLockfile, property.Property.Name == "yarn" && isYarnBerry(ctx, "yarn"))
		candidates = append(candidates, plan.Candidate{
			Value:    run,
			Evidence: property.Property.Evidence,
		})
	}
	return plan.Ambiguity{
		Subject:    "dependencies.install",
		Message:    "Competing lockfiles support different frozen dependency-install commands.",
		Candidates: candidates,
	}
}

func evidenceIncludesLockfile(evidence []plan.Evidence) bool {
	for _, item := range evidence {
		if item.Kind != plan.EvidenceFile {
			continue
		}
		base := path.Base(item.Source)
		for _, lockfile := range lockfiles {
			if base == lockfile.file {
				return true
			}
		}
	}
	return false
}
