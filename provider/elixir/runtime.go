package elixir

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

func runtimeFindings(ctx provider.Context, mixVersion string) ([]plan.Finding, error) {
	var findings []plan.Finding
	if mixVersion != "" {
		findings = append(findings, runtimeFinding(ctx, "elixir", mixVersion, plan.Evidence{
			Kind:    plan.EvidenceDeclaration,
			Source:  ctx.SourcePath("mix.exs"),
			Pointer: "/project/elixir",
		}))
	}

	versions, source, err := readToolVersions(ctx)
	if err != nil {
		return nil, err
	}
	for _, runtime := range []string{"elixir", "erlang"} {
		if version := versions[runtime]; version != "" {
			findings = append(findings, runtimeFinding(ctx, runtime, version, plan.Evidence{
				Kind:    plan.EvidenceDeclaration,
				Source:  source,
				Pointer: "/" + runtime,
			}))
		}
	}
	return findings, nil
}

func runtimeFinding(ctx provider.Context, name, version string, evidence plan.Evidence) plan.Finding {
	return plan.RequirementFinding{
		ProjectPath: ctx.ProjectPath,
		Detector:    providerName,
		Requirement: plan.Requirement{
			Kind:       plan.RequirementRuntime,
			Name:       name,
			Version:    version,
			Confidence: plan.ConfidenceHigh,
			Evidence:   []plan.Evidence{evidence},
		},
	}
}

func readToolVersions(ctx provider.Context) (map[string]string, string, error) {
	dir := ctx.ProjectDir()
	for {
		path := filepath.Join(dir, ".tool-versions")
		contents, err := os.ReadFile(path)
		switch {
		case err == nil:
			relative, relErr := filepath.Rel(ctx.RepositoryRoot, path)
			if relErr != nil {
				return nil, "", fmt.Errorf("resolve .tool-versions source: %w", relErr)
			}
			versions, parseErr := parseToolVersions(string(contents))
			if parseErr != nil {
				return nil, "", fmt.Errorf("parse %s: %w", filepath.ToSlash(relative), parseErr)
			}
			return versions, filepath.ToSlash(relative), nil
		case !os.IsNotExist(err):
			return nil, "", fmt.Errorf("read %s: %w", path, err)
		}
		if dir == ctx.RepositoryRoot || !strings.HasPrefix(dir, ctx.RepositoryRoot+string(filepath.Separator)) {
			return nil, "", nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, "", nil
		}
		dir = parent
	}
}

func parseToolVersions(contents string) (map[string]string, error) {
	versions := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(contents))
	for scanner.Scan() {
		line, _, _ := strings.Cut(scanner.Text(), "#")
		fields := strings.Fields(line)
		if len(fields) >= 2 && versions[fields[0]] == "" {
			versions[fields[0]] = fields[1]
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return versions, nil
}
