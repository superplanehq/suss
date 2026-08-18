// Package golang detects Go modules from go.mod and related files.
package golang

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

const providerName = "go"

// Provider detects Go projects from go.mod and related files.
type Provider struct{}

// Name returns the stable provider identifier.
func (Provider) Name() string {
	return providerName
}

// Detect inspects one project root. It returns an empty result when the
// directory has no go.mod.
func (Provider) Detect(ctx provider.Context) (provider.Result, error) {
	mod, ok, err := readGoMod(ctx)
	if err != nil {
		return provider.Result{}, err
	}
	if !ok {
		return provider.Result{}, nil
	}

	var result provider.Result
	result.Findings = append(result.Findings, languageFinding(ctx, mod))
	if req, ok := runtimeRequirement(ctx, mod); ok {
		result.Findings = append(result.Findings, req)
	}
	if fact, ok := workspaceFinding(ctx); ok {
		result.Findings = append(result.Findings, fact)
	}
	result.Findings = append(result.Findings, toolFindings(ctx)...)

	commands, err := inferredCommands(ctx)
	if err != nil {
		return provider.Result{}, err
	}
	result.Findings = append(result.Findings, commands...)
	return result, nil
}

type goMod struct {
	Module  string
	Version string
}

func readGoMod(ctx provider.Context) (goMod, bool, error) {
	path := filepath.Join(ctx.ProjectDir(), "go.mod")
	contents, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return goMod{}, false, nil
	}
	if err != nil {
		return goMod{}, false, fmt.Errorf("read go.mod: %w", err)
	}
	return parseGoMod(string(contents)), true, nil
}

func parseGoMod(contents string) goMod {
	var parsed goMod
	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		line, _, _ = strings.Cut(line, "//")
		line = strings.TrimSpace(line)
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "module":
			if parsed.Module == "" {
				parsed.Module = strings.Trim(fields[1], `"'`)
			}
		case "go":
			if parsed.Version == "" {
				parsed.Version = fields[1]
			}
		}
	}
	return parsed
}

func languageFinding(ctx provider.Context, mod goMod) plan.Finding {
	evidence := plan.Evidence{
		Kind:   plan.EvidenceDeclaration,
		Source: ctx.SourcePath("go.mod"),
	}
	if mod.Module != "" {
		evidence.Pointer = "/module"
	}
	return plan.PropertyFinding{
		ProjectPath: ctx.ProjectPath,
		Detector:    providerName,
		Property: plan.Property{
			Kind:       plan.PropertyLanguage,
			Name:       "go",
			Confidence: plan.ConfidenceHigh,
			Evidence:   []plan.Evidence{evidence},
		},
	}
}

func runtimeRequirement(ctx provider.Context, mod goMod) (plan.Finding, bool) {
	if mod.Version == "" {
		return nil, false
	}
	return plan.RequirementFinding{
		ProjectPath: ctx.ProjectPath,
		Detector:    providerName,
		Requirement: plan.Requirement{
			Kind:       plan.RequirementRuntime,
			Name:       "go",
			Version:    mod.Version,
			Confidence: plan.ConfidenceHigh,
			Evidence: []plan.Evidence{{
				Kind:    plan.EvidenceDeclaration,
				Source:  ctx.SourcePath("go.mod"),
				Pointer: "/go",
			}},
		},
	}, true
}

func workspaceFinding(ctx provider.Context) (plan.Finding, bool) {
	if !fileExists(ctx.ProjectDir(), "go.work") {
		return nil, false
	}
	return plan.PropertyFinding{
		ProjectPath: ctx.ProjectPath,
		Detector:    providerName,
		Property: plan.Property{
			Kind:       plan.PropertyFact,
			Name:       "workspace.orchestrator",
			Value:      "go",
			Confidence: plan.ConfidenceHigh,
			Evidence: []plan.Evidence{{
				Kind:   plan.EvidenceConfiguration,
				Source: ctx.SourcePath("go.work"),
			}},
		},
	}, true
}

var golangciLintFiles = []string{
	".golangci.yml",
	".golangci.yaml",
	".golangci.toml",
	".golangci.json",
}

func toolFindings(ctx provider.Context) []plan.Finding {
	var evidence []plan.Evidence
	for _, name := range golangciLintFiles {
		if !fileExists(ctx.ProjectDir(), name) {
			continue
		}
		evidence = append(evidence, plan.Evidence{
			Kind:   plan.EvidenceConfiguration,
			Source: ctx.SourcePath(name),
		})
	}
	if len(evidence) == 0 {
		return nil
	}
	return []plan.Finding{plan.PropertyFinding{
		ProjectPath: ctx.ProjectPath,
		Detector:    providerName,
		Property: plan.Property{
			Kind:       plan.PropertyFact,
			Name:       "tool.configured",
			Value:      "golangci-lint",
			Confidence: plan.ConfidenceHigh,
			Evidence:   evidence,
		},
	}}
}

func fileExists(dir, name string) bool {
	info, err := os.Stat(filepath.Join(dir, name))
	return err == nil && !info.IsDir()
}

func stringPtr(value string) *string {
	return &value
}

func walkSkipDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "vendor", "testdata", "node_modules", "_build", "deps", "dist", "target":
		return true
	default:
		return false
	}
}

func findTestFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path == root {
				return nil
			}
			if walkSkipDir(entry.Name()) || entry.Type()&os.ModeSymlink != 0 {
				return filepath.SkipDir
			}
			if fileExists(path, "go.mod") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(entry.Name(), "_test.go") {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	return files, err
}
