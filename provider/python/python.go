// Package python detects Python projects from pyproject.toml, setup.py, and
// Pipfile. Detection is static and never evaluates Python.
package python

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

const providerName = "python"

// Provider detects one Python project.
type Provider struct{}

// Name returns the stable provider identifier.
func (Provider) Name() string {
	return providerName
}

// Detect inspects one project root. It returns no findings without a Python
// identity file (pyproject.toml, setup.py, or Pipfile).
func (Provider) Detect(ctx provider.Context) (provider.Result, error) {
	project, ok, err := readProject(ctx)
	if err != nil || !ok {
		return provider.Result{}, err
	}

	choice := choosePackageManager(ctx, project)

	result := provider.Result{
		Findings:    projectFindings(ctx, project, choice),
		Ambiguities: choice.ambiguities,
	}
	runtimes, conflicts, err := runtimeFindings(ctx, project.RequiresPython)
	if err != nil {
		return provider.Result{}, err
	}
	result.Findings = append(result.Findings, runtimes...)
	result.Conflicts = append(result.Conflicts, conflicts...)
	result.Findings = append(result.Findings, configuredToolFindings(ctx, project)...)

	commands, err := commandFindings(ctx, project, choice)
	if err != nil {
		return provider.Result{}, err
	}
	result.Findings = append(result.Findings, commands...)
	return result, nil
}

func readProject(ctx provider.Context) (pythonProject, bool, error) {
	var project pythonProject
	found := false

	if contents, ok, err := readFile(ctx, "pyproject.toml"); err != nil {
		return pythonProject{}, false, err
	} else if ok {
		project = parsePyproject(contents)
		found = true
	}

	if contents, ok, err := readFile(ctx, "Pipfile"); err != nil {
		return pythonProject{}, false, err
	} else if ok {
		parsed := parsePipfile(contents)
		if found {
			project = mergeProject(project, parsed)
		} else {
			project = parsed
			found = true
		}
	}

	if contents, ok, err := readFile(ctx, "setup.py"); err != nil {
		return pythonProject{}, false, err
	} else if ok {
		parsed := parseSetupPy(contents)
		if found {
			project = mergeProject(project, parsed)
		} else {
			project = parsed
			found = true
		}
	}

	if !found {
		return pythonProject{}, false, nil
	}

	extras, err := readRequirements(ctx)
	if err != nil {
		return pythonProject{}, false, err
	}
	addDependencies(&project, "requirements.txt", "", extras)
	return project, true, nil
}

func readFile(ctx provider.Context, name string) (string, bool, error) {
	contents, err := os.ReadFile(filepath.Join(ctx.ProjectDir(), name))
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read %s: %w", name, err)
	}
	return string(contents), true, nil
}

func readRequirements(ctx provider.Context) ([]string, error) {
	contents, ok, err := readFile(ctx, "requirements.txt")
	if err != nil || !ok {
		return nil, err
	}
	return parseRequirements(contents), nil
}

func projectFindings(ctx provider.Context, project pythonProject, choice managerChoice) []plan.Finding {
	source := ctx.SourcePath(project.Manifest)
	declaration := []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: source}}
	findings := []plan.Finding{
		propertyFinding(ctx, plan.PropertyLanguage, "python", "", declaration),
	}
	findings = append(findings, choice.findings...)
	for _, name := range knownFrameworks {
		dep, ok := project.Dependencies[name]
		if !ok {
			continue
		}
		findings = append(findings, propertyFinding(ctx, plan.PropertyFramework, name, "", []plan.Evidence{{
			Kind:        plan.EvidenceDeclaration,
			Source:      ctx.SourcePath(depSourceFile(dep, project.Manifest)),
			Pointer:     depPointer(name),
			Description: frameworkDescription(name),
		}}))
	}
	return findings
}

func depSourceFile(dep depDeclaration, fallback string) string {
	if dep.Source == "" {
		return fallback
	}
	file, _, ok := strings.Cut(dep.Source, "/")
	if !ok {
		return dep.Source
	}
	if file == "" {
		return fallback
	}
	return file
}

func frameworkDescription(name string) string {
	switch name {
	case "django":
		return "The project declares Django."
	case "flask":
		return "The project declares Flask."
	case "fastapi":
		return "The project declares FastAPI."
	default:
		return "The project declares " + name + "."
	}
}

func propertyFinding(ctx provider.Context, kind plan.PropertyKind, name, version string, evidence []plan.Evidence) plan.Finding {
	return plan.PropertyFinding{
		ProjectPath: ctx.ProjectPath,
		Detector:    providerName,
		Property: plan.Property{
			Kind:       kind,
			Name:       name,
			Version:    version,
			Confidence: plan.ConfidenceHigh,
			Evidence:   evidence,
		},
	}
}

func factFinding(ctx provider.Context, name, value string, evidence []plan.Evidence) plan.Finding {
	return plan.PropertyFinding{
		ProjectPath: ctx.ProjectPath,
		Detector:    providerName,
		Property: plan.Property{
			Kind:       plan.PropertyFact,
			Name:       name,
			Value:      value,
			Confidence: plan.ConfidenceHigh,
			Evidence:   evidence,
		},
	}
}

func stringPtr(value string) *string {
	return &value
}

func fileExists(dir, name string) bool {
	info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name)))
	return err == nil && !info.IsDir()
}

func walkSkipDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "venv", "virtualenv", "env", "site-packages", "__pycache__", "node_modules", "vendor", "_build", "deps", "dist", "build", "target", "tmp", "htmlcov", ".eggs":
		return true
	default:
		return strings.HasSuffix(name, ".egg-info")
	}
}

func firstFile(root string, match func(name string) bool) (string, error) {
	var first string
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
			if path != root && pythonIdentity(path) {
				return filepath.SkipDir
			}
			return nil
		}
		if first != "" || entry.Type()&os.ModeSymlink != 0 || !match(entry.Name()) {
			return nil
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		first = filepath.ToSlash(relative)
		return fs.SkipAll
	})
	if err != nil {
		return "", err
	}
	return first, nil
}

func pythonIdentity(dir string) bool {
	for _, name := range []string{"pyproject.toml", "setup.py", "Pipfile"} {
		if fileExists(dir, name) {
			return true
		}
	}
	return false
}
