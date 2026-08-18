package python

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

type configuredTool struct {
	name         string
	files        []string
	tables       []string
	dependencies []string
	prefix       bool
	iniFiles     []string
	iniSections  []string
}

var defaultINIFiles = []string{"setup.cfg", "tox.ini"}

var configuredTools = []configuredTool{
	{name: "pytest", files: []string{"pytest.ini", "pytest.toml", ".pytest.ini", "conftest.py"}, tables: []string{"pytest"}, dependencies: []string{"pytest", "pytest-django"}, prefix: true, iniFiles: defaultINIFiles, iniSections: []string{"tool:pytest", "pytest"}},
	{name: "ruff", files: []string{"ruff.toml", ".ruff.toml"}, tables: []string{"ruff"}, dependencies: []string{"ruff"}},
	{name: "black", files: []string{".black"}, tables: []string{"black"}, dependencies: []string{"black"}},
	{name: "mypy", files: []string{"mypy.ini", ".mypy.ini"}, tables: []string{"mypy"}, dependencies: []string{"mypy"}, iniFiles: defaultINIFiles, iniSections: []string{"mypy"}},
	{name: "pyright", files: []string{"pyrightconfig.json"}, tables: []string{"pyright"}, dependencies: []string{"pyright"}},
	{name: "flake8", files: []string{".flake8"}, tables: []string{"flake8"}, dependencies: []string{"flake8"}, iniFiles: defaultINIFiles, iniSections: []string{"flake8"}},
	{name: "pylint", files: []string{".pylintrc", "pylintrc"}, tables: []string{"pylint"}, dependencies: []string{"pylint"}, iniFiles: defaultINIFiles, iniSections: []string{"pylint"}},
	{name: "isort", files: []string{".isort.cfg"}, tables: []string{"isort"}, dependencies: []string{"isort"}, iniFiles: defaultINIFiles, iniSections: []string{"isort"}},
	{name: "tox", files: []string{"tox.ini"}, tables: []string{"tox"}, dependencies: []string{"tox"}},
	{name: "nox", files: []string{"noxfile.py"}, tables: []string{"nox"}, dependencies: []string{"nox"}},
}

func configuredToolFindings(ctx provider.Context, project pythonProject) []plan.Finding {
	var findings []plan.Finding
	for _, tool := range configuredTools {
		evidence := configuredToolEvidence(ctx, project, tool)
		if len(evidence) == 0 {
			continue
		}
		findings = append(findings, factFinding(ctx, "tool.configured", tool.name, evidence))
	}
	return findings
}

func configuredToolEvidence(ctx provider.Context, project pythonProject, tool configuredTool) []plan.Evidence {
	var evidence []plan.Evidence
	for _, name := range tool.files {
		if fileExists(ctx.ProjectDir(), name) {
			evidence = append(evidence, plan.Evidence{Kind: plan.EvidenceConfiguration, Source: ctx.SourcePath(name)})
		}
	}
	evidence = append(evidence, iniSectionEvidence(ctx, tool)...)
	for _, table := range tool.tables {
		if _, ok := project.ToolTables[table]; ok {
			evidence = append(evidence, plan.Evidence{
				Kind:    plan.EvidenceConfiguration,
				Source:  ctx.SourcePath(project.Manifest),
				Pointer: "/tool/" + table,
			})
		}
	}
	for _, name := range tool.dependencies {
		dep, ok := project.Dependencies[normalizeDependency(name)]
		if !ok && tool.prefix {
			dep, ok = prefixedDependency(project, name)
		}
		if !ok {
			continue
		}
		evidence = append(evidence, plan.Evidence{
			Kind:    plan.EvidenceDeclaration,
			Source:  ctx.SourcePath(depSourceFile(dep, project.Manifest)),
			Pointer: depPointer(dep.Name),
		})
	}
	return evidence
}

func prefixedDependency(project pythonProject, prefix string) (depDeclaration, bool) {
	prefix = normalizeDependency(prefix)
	var best depDeclaration
	found := false
	for name, dep := range project.Dependencies {
		if name != prefix && !strings.HasPrefix(name, prefix+"-") {
			continue
		}
		if !found || name < best.Name {
			best = dep
			found = true
		}
	}
	return best, found
}

func toolByName(name string) (configuredTool, bool) {
	for _, tool := range configuredTools {
		if tool.name == name {
			return tool, true
		}
	}
	return configuredTool{}, false
}

func iniSectionEvidence(ctx provider.Context, tool configuredTool) []plan.Evidence {
	if len(tool.iniSections) == 0 {
		return nil
	}
	var evidence []plan.Evidence
	for _, name := range tool.iniFiles {
		contents, err := os.ReadFile(filepath.Join(ctx.ProjectDir(), name))
		if err != nil {
			continue
		}
		section, ok := iniMatchingSection(string(contents), tool.iniSections...)
		if !ok {
			continue
		}
		evidence = append(evidence, plan.Evidence{
			Kind:    plan.EvidenceConfiguration,
			Source:  ctx.SourcePath(name),
			Pointer: "/" + section,
		})
	}
	return evidence
}

func iniMatchingSection(contents string, sections ...string) (string, bool) {
	wanted := make(map[string]string, len(sections))
	for _, section := range sections {
		wanted[strings.ToLower(section)] = section
	}
	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 3 || line[0] != '[' || line[len(line)-1] != ']' {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
		if section, ok := wanted[name]; ok {
			return section, true
		}
		for key, section := range wanted {
			if strings.HasPrefix(name, key+".") {
				return section, true
			}
		}
	}
	return "", false
}
