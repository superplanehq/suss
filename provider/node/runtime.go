package node

import (
	"encoding/json"
	"strings"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

type runtimeResult struct {
	findings  []plan.Finding
	conflicts []plan.Conflict
}

type versionPin struct {
	version  string
	evidence plan.Evidence
}

func runtimeRequirements(ctx provider.Context, manifest packageManifest) (runtimeResult, error) {
	var pins []versionPin

	nvmrc, err := readVersionFile(ctx, ".nvmrc")
	if err != nil {
		return runtimeResult{}, err
	}
	if nvmrc != nil {
		pins = append(pins, *nvmrc)
	}

	nodeVersion, err := readVersionFile(ctx, ".node-version")
	if err != nil {
		return runtimeResult{}, err
	}
	if nodeVersion != nil {
		pins = append(pins, *nodeVersion)
	}

	engines := enginesNode(ctx, manifest)

	if disagreeingPins(pins) {
		return conflictingPins(ctx, pins, engines), nil
	}

	findings := make([]plan.Finding, 0, 2)
	for _, requirement := range mergeRuntime(pins, engines) {
		findings = append(findings, plan.RequirementFinding{
			ProjectPath: ctx.ProjectPath,
			Detector:    providerName,
			Requirement: requirement,
		})
	}
	return runtimeResult{findings: findings}, nil
}

func readVersionFile(ctx provider.Context, name string) (*versionPin, error) {
	contents, ok, err := readFile(ctx.ProjectDir(), name)
	if err != nil || !ok {
		return nil, err
	}
	version := firstVersionLine(contents)
	if version == "" {
		return nil, nil
	}
	return &versionPin{
		version: version,
		evidence: plan.Evidence{
			Kind:   plan.EvidenceDeclaration,
			Source: ctx.SourcePath(name),
		},
	}, nil
}

func firstVersionLine(contents string) string {
	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line, _, _ = strings.Cut(line, "#")
		return strings.TrimSpace(line)
	}
	return ""
}

func enginesNode(ctx provider.Context, manifest packageManifest) *versionPin {
	raw, ok := manifest.Engines["node"]
	if !ok {
		return nil
	}
	var version string
	if err := json.Unmarshal(raw, &version); err != nil || version == "" {
		return nil
	}
	return &versionPin{
		version: version,
		evidence: plan.Evidence{
			Kind:    plan.EvidenceDeclaration,
			Source:  ctx.SourcePath("package.json"),
			Pointer: "/engines/node",
		},
	}
}

func disagreeingPins(pins []versionPin) bool {
	if len(pins) < 2 {
		return false
	}
	return pins[0].version != pins[1].version
}

func conflictingPins(ctx provider.Context, pins []versionPin, engines *versionPin) runtimeResult {
	assertions := make([]plan.Candidate, 0, len(pins))
	findings := make([]plan.Finding, 0, len(pins))
	for _, pin := range pins {
		assertions = append(assertions, plan.Candidate{
			Value:    pin.version,
			Evidence: []plan.Evidence{pin.evidence},
		})
		findings = append(findings, plan.RequirementFinding{
			ProjectPath: ctx.ProjectPath,
			Detector:    providerName,
			Requirement: plan.Requirement{
				Kind:       plan.RequirementRuntime,
				Name:       "node",
				Version:    pin.version,
				Confidence: plan.ConfidenceMedium,
				Evidence:   []plan.Evidence{pin.evidence},
			},
		})
	}
	if engines != nil {
		findings = append(findings, plan.RequirementFinding{
			ProjectPath: ctx.ProjectPath,
			Detector:    providerName,
			Requirement: plan.Requirement{
				Kind:       plan.RequirementRuntime,
				Name:       "node",
				Version:    engines.version,
				Confidence: plan.ConfidenceHigh,
				Evidence:   []plan.Evidence{engines.evidence},
			},
		})
	}
	return runtimeResult{
		findings: findings,
		conflicts: []plan.Conflict{{
			Subject:    "runtime.node.version",
			Message:    ".nvmrc and .node-version pin different Node.js versions.",
			Assertions: assertions,
		}},
	}
}

func mergeRuntime(pins []versionPin, engines *versionPin) []plan.Requirement {
	var pinEvidence []plan.Evidence
	pinVersion := ""
	if len(pins) > 0 {
		pinVersion = pins[0].version
		for _, pin := range pins {
			pinEvidence = append(pinEvidence, pin.evidence)
		}
	}

	var requirements []plan.Requirement
	if pinVersion != "" {
		evidence := pinEvidence
		if engines != nil && engines.version == pinVersion {
			evidence = append(append([]plan.Evidence{}, pinEvidence...), engines.evidence)
			engines = nil
		}
		requirements = append(requirements, plan.Requirement{
			Kind:       plan.RequirementRuntime,
			Name:       "node",
			Version:    pinVersion,
			Confidence: plan.ConfidenceHigh,
			Evidence:   evidence,
		})
	}
	if engines != nil {
		requirements = append(requirements, plan.Requirement{
			Kind:       plan.RequirementRuntime,
			Name:       "node",
			Version:    engines.version,
			Confidence: plan.ConfidenceHigh,
			Evidence:   []plan.Evidence{engines.evidence},
		})
	}
	return requirements
}
