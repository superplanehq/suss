// Package suss inspects a repository and reports how to set it up, build it,
// test it, and run it. Detection is static: it does not execute commands or
// install dependencies.
package suss

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
	"github.com/superplanehq/suss/provider/node"
)

var detectors = []provider.Provider{
	node.Provider{},
}

// Providers returns the names of detectors that run during Detect.
func Providers() []string {
	names := make([]string, len(detectors))
	for i, detector := range detectors {
		names[i] = detector.Name()
	}
	return names
}

// Detect inspects root and returns a schema-versioned plan document.
func Detect(root string) (plan.Document, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return plan.Document{}, fmt.Errorf("detect %s: %w", root, err)
	}

	info, err := os.Stat(absolute)
	if err != nil {
		return plan.Document{}, fmt.Errorf("detect %s: %w", root, err)
	}
	if !info.IsDir() {
		return plan.Document{}, fmt.Errorf("detect %s: not a directory", root)
	}

	paths, err := findProjectRoots(absolute)
	if err != nil {
		return plan.Document{}, fmt.Errorf("detect %s: %w", root, err)
	}

	projects := make([]plan.ProjectPlan, 0, len(paths))
	for _, path := range paths {
		project, err := detectProject(absolute, path)
		if err != nil {
			return plan.Document{}, fmt.Errorf("detect %s: %w", path, err)
		}
		projects = append(projects, project)
	}

	document := plan.NewDocument(projects)
	document.Sort()
	if err := document.Validate(); err != nil {
		return plan.Document{}, fmt.Errorf("detect %s: invalid plan: %w", root, err)
	}
	return document, nil
}

func detectProject(repositoryRoot, path string) (plan.ProjectPlan, error) {
	ctx := provider.Context{RepositoryRoot: repositoryRoot, ProjectPath: path}
	var combined provider.Result
	for _, detector := range detectors {
		result, err := detector.Detect(ctx)
		if err != nil {
			return plan.ProjectPlan{}, fmt.Errorf("%s: %w", detector.Name(), err)
		}
		combined.Findings = append(combined.Findings, result.Findings...)
		combined.Ambiguities = append(combined.Ambiguities, result.Ambiguities...)
		combined.Conflicts = append(combined.Conflicts, result.Conflicts...)
	}

	project := assemble(path, combined)
	if fact, ok := fixtureRoleFact(path); ok {
		project.Facts = append(project.Facts, fact)
	}
	return project, nil
}
