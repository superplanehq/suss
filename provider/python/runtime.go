package python

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

type runtimePin struct {
	version  string
	evidence plan.Evidence
}

func runtimeFindings(ctx provider.Context, requiresPython string) ([]plan.Finding, []plan.Conflict, error) {
	var pins []runtimePin
	pythonVersion, err := readAncestorPythonVersion(ctx)
	if err != nil {
		return nil, nil, err
	}
	if pythonVersion != nil {
		pins = append(pins, *pythonVersion)
	}
	toolVersion, err := readAncestorToolVersion(ctx)
	if err != nil {
		return nil, nil, err
	}
	if toolVersion != nil {
		pins = append(pins, *toolVersion)
	}

	findings := make([]plan.Finding, 0, len(pins)+1)
	var conflicts []plan.Conflict
	requiresMerged := false
	if pinsDisagree(pins) {
		assertions := make([]plan.Candidate, 0, len(pins))
		for _, pin := range pins {
			assertions = append(assertions, plan.Candidate{Value: pin.version, Evidence: []plan.Evidence{pin.evidence}})
			evidence := []plan.Evidence{pin.evidence}
			if pin.version == requiresPython {
				evidence = append(evidence, requiresPythonEvidence(ctx))
				requiresMerged = true
			}
			findings = append(findings, runtimeFinding(ctx, pin.version, plan.ConfidenceMedium, evidence))
		}
		conflicts = append(conflicts, plan.Conflict{
			Subject:    "runtime.python.version",
			Message:    ".python-version and .tool-versions pin different Python versions.",
			Assertions: assertions,
		})
	} else if len(pins) > 0 {
		evidence := make([]plan.Evidence, 0, len(pins))
		for _, pin := range pins {
			evidence = append(evidence, pin.evidence)
		}
		if pins[0].version == requiresPython {
			evidence = append(evidence, requiresPythonEvidence(ctx))
			requiresMerged = true
		}
		findings = append(findings, runtimeFinding(ctx, pins[0].version, plan.ConfidenceHigh, evidence))
	}

	if requiresPython != "" && !requiresMerged {
		findings = append(findings, runtimeFinding(ctx, requiresPython, plan.ConfidenceHigh, []plan.Evidence{requiresPythonEvidence(ctx)}))
	}
	return findings, conflicts, nil
}

func readAncestorPythonVersion(ctx provider.Context) (*runtimePin, error) {
	path, contents, ok, err := readAncestorFile(ctx, ".python-version")
	if err != nil || !ok {
		return nil, err
	}
	version := firstVersionLine(contents)
	if version == "" {
		return nil, nil
	}
	return &runtimePin{version: version, evidence: plan.Evidence{Kind: plan.EvidenceDeclaration, Source: path}}, nil
}

func readAncestorToolVersion(ctx provider.Context) (*runtimePin, error) {
	path, contents, ok, err := readAncestorFile(ctx, ".tool-versions")
	if err != nil || !ok {
		return nil, err
	}
	scanner := bufio.NewScanner(strings.NewReader(contents))
	for scanner.Scan() {
		line, _, _ := strings.Cut(scanner.Text(), "#")
		fields := strings.Fields(line)
		if len(fields) >= 2 && (fields[0] == "python" || fields[0] == "python3") {
			return &runtimePin{
				version:  fields[1],
				evidence: plan.Evidence{Kind: plan.EvidenceDeclaration, Source: path, Pointer: "/python"},
			}, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return nil, nil
}

func readAncestorFile(ctx provider.Context, name string) (string, string, bool, error) {
	dir := ctx.ProjectDir()
	for {
		path := filepath.Join(dir, name)
		contents, err := os.ReadFile(path)
		switch {
		case err == nil:
			relative, relErr := filepath.Rel(ctx.RepositoryRoot, path)
			if relErr != nil {
				return "", "", false, fmt.Errorf("resolve %s source: %w", name, relErr)
			}
			return filepath.ToSlash(relative), string(contents), true, nil
		case !os.IsNotExist(err):
			return "", "", false, fmt.Errorf("read %s: %w", path, err)
		}
		if dir == ctx.RepositoryRoot || !strings.HasPrefix(dir, ctx.RepositoryRoot+string(filepath.Separator)) {
			return "", "", false, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", false, nil
		}
		dir = parent
	}
}

func firstVersionLine(contents string) string {
	for _, line := range strings.Split(contents, "\n") {
		line, _, _ = strings.Cut(line, "#")
		if version := strings.TrimSpace(line); version != "" {
			return version
		}
	}
	return ""
}

func pinsDisagree(pins []runtimePin) bool {
	for index := 1; index < len(pins); index++ {
		if pins[index].version != pins[0].version {
			return true
		}
	}
	return false
}

func requiresPythonEvidence(ctx provider.Context) plan.Evidence {
	source := "pyproject.toml"
	if fileExists(ctx.ProjectDir(), "Pipfile") && !fileExists(ctx.ProjectDir(), "pyproject.toml") {
		source = "Pipfile"
	}
	return plan.Evidence{
		Kind:    plan.EvidenceDeclaration,
		Source:  ctx.SourcePath(source),
		Pointer: "/requires-python",
	}
}

func runtimeFinding(ctx provider.Context, version string, confidence plan.Confidence, evidence []plan.Evidence) plan.Finding {
	return plan.RequirementFinding{
		ProjectPath: ctx.ProjectPath,
		Detector:    providerName,
		Requirement: plan.Requirement{
			Kind:       plan.RequirementRuntime,
			Name:       "python",
			Version:    version,
			Confidence: confidence,
			Evidence:   evidence,
		},
	}
}
