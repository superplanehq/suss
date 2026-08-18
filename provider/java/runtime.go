package java

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

func runtimeFindings(ctx provider.Context, project javaProject) ([]plan.Finding, []plan.Conflict, error) {
	var pins []runtimePin
	for _, reader := range []func(provider.Context) (*runtimePin, error){
		readJavaVersionFile,
		readJavaToolVersion,
		readSdkmanrc,
	} {
		pin, err := reader(ctx)
		if err != nil {
			return nil, nil, err
		}
		if pin != nil {
			pins = append(pins, *pin)
		}
	}

	findings := make([]plan.Finding, 0, len(pins)+2)
	var conflicts []plan.Conflict
	manifestMerged := map[string]bool{}

	if pinsDisagree(pins) {
		assertions := make([]plan.Candidate, 0, len(pins))
		for _, pin := range pins {
			assertions = append(assertions, plan.Candidate{Value: pin.version, Evidence: []plan.Evidence{pin.evidence}})
			evidence := []plan.Evidence{pin.evidence}
			evidence = append(evidence, matchingManifestEvidence(ctx, project, pin.version, manifestMerged)...)
			findings = append(findings, runtimeFinding(ctx, pin.version, plan.ConfidenceMedium, evidence))
		}
		conflicts = append(conflicts, plan.Conflict{
			Subject:    "runtime.java.version",
			Message:    "Java version files pin different versions.",
			Assertions: assertions,
		})
	} else if len(pins) > 0 {
		evidence := make([]plan.Evidence, 0, len(pins)+2)
		for _, pin := range pins {
			evidence = append(evidence, pin.evidence)
		}
		evidence = append(evidence, matchingManifestEvidence(ctx, project, pins[0].version, manifestMerged)...)
		findings = append(findings, runtimeFinding(ctx, pins[0].version, plan.ConfidenceHigh, evidence))
	}

	for _, version := range manifestJavaVersions(ctx, project) {
		if manifestMerged[version.value] {
			continue
		}
		findings = append(findings, runtimeFinding(ctx, version.value, plan.ConfidenceHigh, []plan.Evidence{version.evidence}))
	}
	return findings, conflicts, nil
}

type manifestVersion struct {
	value    string
	evidence plan.Evidence
}

func manifestJavaVersions(ctx provider.Context, project javaProject) []manifestVersion {
	var versions []manifestVersion
	if project.Maven != nil && project.Maven.JavaVersion != "" {
		versions = append(versions, manifestVersion{
			value: project.Maven.JavaVersion,
			evidence: plan.Evidence{
				Kind:    plan.EvidenceDeclaration,
				Source:  ctx.SourcePath(project.Maven.Source),
				Pointer: project.Maven.JavaVersionPtr,
			},
		})
	}
	if project.Gradle != nil && project.Gradle.JavaVersion != "" {
		versions = append(versions, manifestVersion{
			value: project.Gradle.JavaVersion,
			evidence: plan.Evidence{
				Kind:    plan.EvidenceDeclaration,
				Source:  ctx.SourcePath(project.Gradle.Source),
				Pointer: project.Gradle.JavaVersionPtr,
			},
		})
	}
	return versions
}

func matchingManifestEvidence(ctx provider.Context, project javaProject, version string, merged map[string]bool) []plan.Evidence {
	var evidence []plan.Evidence
	for _, item := range manifestJavaVersions(ctx, project) {
		if item.value != version {
			continue
		}
		merged[item.value] = true
		evidence = append(evidence, item.evidence)
	}
	return evidence
}

func readJavaVersionFile(ctx provider.Context) (*runtimePin, error) {
	path, contents, ok, err := readAncestorFile(ctx, ".java-version")
	if err != nil || !ok {
		return nil, err
	}
	version := firstVersionLine(contents)
	version = strings.TrimPrefix(version, "java-")
	if version == "" {
		return nil, nil
	}
	return &runtimePin{version: version, evidence: plan.Evidence{Kind: plan.EvidenceDeclaration, Source: path}}, nil
}

func readJavaToolVersion(ctx provider.Context) (*runtimePin, error) {
	path, contents, ok, err := readAncestorFile(ctx, ".tool-versions")
	if err != nil || !ok {
		return nil, err
	}
	scanner := bufio.NewScanner(strings.NewReader(contents))
	for scanner.Scan() {
		line, _, _ := strings.Cut(scanner.Text(), "#")
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "java" {
			return &runtimePin{
				version:  fields[1],
				evidence: plan.Evidence{Kind: plan.EvidenceDeclaration, Source: path, Pointer: "/java"},
			}, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return nil, nil
}

func readSdkmanrc(ctx provider.Context) (*runtimePin, error) {
	path, contents, ok, err := readAncestorFile(ctx, ".sdkmanrc")
	if err != nil || !ok {
		return nil, err
	}
	scanner := bufio.NewScanner(strings.NewReader(contents))
	for scanner.Scan() {
		line, _, _ := strings.Cut(scanner.Text(), "#")
		line = strings.TrimSpace(line)
		name, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(name) != "java" {
			continue
		}
		version := strings.TrimSpace(value)
		if version == "" {
			return nil, nil
		}
		return &runtimePin{
			version:  version,
			evidence: plan.Evidence{Kind: plan.EvidenceDeclaration, Source: path, Pointer: "/java"},
		}, nil
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

func runtimeFinding(ctx provider.Context, version string, confidence plan.Confidence, evidence []plan.Evidence) plan.Finding {
	return plan.RequirementFinding{
		ProjectPath: ctx.ProjectPath,
		Detector:    providerName,
		Requirement: plan.Requirement{
			Kind:       plan.RequirementRuntime,
			Name:       "java",
			Version:    version,
			Confidence: confidence,
			Evidence:   evidence,
		},
	}
}
