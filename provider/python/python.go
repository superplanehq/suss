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
	runtimes, conflicts, err := runtimeFindings(ctx, project)
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
	addDependencies(&project, "requirements.txt", "", extras, depOrigin{Kind: depKindMain, Manager: "pip"})
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

func walkSkipDir(path, name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "venv", "virtualenv", "site-packages", "__pycache__", "node_modules", "vendor", "_build", "deps", "dist", "build", "target", "tmp", "htmlcov", ".eggs":
		return true
	}
	if strings.HasSuffix(name, ".egg-info") {
		return true
	}
	return isVirtualEnvDir(path)
}

func isVirtualEnvDir(path string) bool {
	info, err := os.Stat(filepath.Join(path, "pyvenv.cfg"))
	return err == nil && !info.IsDir()
}

func firstPythonTestAt(root, rel string) (string, error) {
	full := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Stat(full)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	if !info.IsDir() {
		if isPythonTestName(info.Name()) {
			return rel, nil
		}
		return "", nil
	}
	path, err := firstFile(full, isPythonTestName)
	if err != nil || path == "" {
		return path, err
	}
	return pathJoinSlash(rel, path), nil
}

func firstFileUnderDirNames(root string, dirNames []string, match func(name string) bool) (string, error) {
	wanted := make(map[string]struct{}, len(dirNames))
	for _, name := range dirNames {
		wanted[name] = struct{}{}
	}
	var first string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path == root {
				return nil
			}
			if walkSkipDir(path, entry.Name()) || entry.Type()&os.ModeSymlink != 0 {
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
		relative = filepath.ToSlash(relative)
		if !pathHasDirName(relative, wanted) {
			return nil
		}
		first = relative
		return fs.SkipAll
	})
	if err != nil {
		return "", err
	}
	return first, nil
}

func pathHasDirName(rel string, names map[string]struct{}) bool {
	for _, part := range strings.Split(rel, "/") {
		if _, ok := names[part]; ok {
			return true
		}
	}
	return false
}

func pathJoinSlash(parts ...string) string {
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(part, "/")
		if part != "" {
			cleaned = append(cleaned, part)
		}
	}
	return strings.Join(cleaned, "/")
}

func configuredPytestTestPaths(root string) []string {
	for _, name := range []string{"pytest.ini", ".pytest.ini", "pytest.toml", "pyproject.toml", "tox.ini", "setup.cfg"} {
		contents, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			continue
		}
		var paths []string
		switch name {
		case "pytest.toml", "pyproject.toml":
			paths = tomlPytestTestPaths(string(contents))
		case "setup.cfg":
			paths = iniPytestTestPaths(string(contents), "tool:pytest", "pytest")
		default:
			paths = iniPytestTestPaths(string(contents), "pytest")
		}
		if cleaned := sanitizeProjectPaths(paths); len(cleaned) > 0 {
			return cleaned
		}
	}
	return nil
}

func tomlPytestTestPaths(contents string) []string {
	doc := parseTOML(contents)
	for _, name := range []string{"tool.pytest.ini_options", "pytest"} {
		section, ok := doc[name]
		if !ok {
			continue
		}
		if values := section.arrays["testpaths"]; len(values) > 0 {
			return values
		}
		if scalar := strings.TrimSpace(section.scalars["testpaths"]); scalar != "" {
			return strings.Fields(scalar)
		}
	}
	return nil
}

func iniPytestTestPaths(contents string, sections ...string) []string {
	value, ok := iniSectionKey(contents, "testpaths", sections...)
	if !ok {
		return nil
	}
	return strings.Fields(value)
}

func iniSectionKey(contents, key string, sections ...string) (string, bool) {
	wanted := make(map[string]struct{}, len(sections))
	for _, section := range sections {
		wanted[strings.ToLower(section)] = struct{}{}
	}
	current := ""
	key = strings.ToLower(key)
	for _, line := range strings.Split(contents, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		if len(trimmed) >= 3 && trimmed[0] == '[' && trimmed[len(trimmed)-1] == ']' {
			current = strings.ToLower(strings.TrimSpace(trimmed[1 : len(trimmed)-1]))
			continue
		}
		if _, ok := wanted[current]; !ok {
			continue
		}
		name, value, found := strings.Cut(trimmed, "=")
		if !found || strings.ToLower(strings.TrimSpace(name)) != key {
			continue
		}
		return strings.TrimSpace(value), true
	}
	return "", false
}

func sanitizeProjectPaths(values []string) []string {
	var paths []string
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		rel, ok := projectRelativePath(value)
		if !ok {
			continue
		}
		if _, dup := seen[rel]; dup {
			continue
		}
		seen[rel] = struct{}{}
		paths = append(paths, rel)
	}
	return paths
}

func projectRelativePath(value string) (string, bool) {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"'`)
	if value == "" || value == "." {
		return "", false
	}
	if filepath.IsAbs(value) {
		return "", false
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	if clean == "." || strings.HasPrefix(clean, "../") || clean == ".." {
		return "", false
	}
	return clean, true
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
			if walkSkipDir(path, entry.Name()) || entry.Type()&os.ModeSymlink != 0 {
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
