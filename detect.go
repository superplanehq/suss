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
	"github.com/superplanehq/suss/provider/gha"
	"github.com/superplanehq/suss/provider/golang"
	"github.com/superplanehq/suss/provider/node"
	"github.com/superplanehq/suss/reconcile"
)

var projectProviders = []provider.Provider{
	node.Provider{},
	golang.Provider{},
}

var repositoryProviders = []provider.Provider{
	gha.Provider{},
}

// Providers returns the names of detectors that run during Detect.
func Providers() []string {
	names := make([]string, 0, len(projectProviders)+len(repositoryProviders))
	for _, detector := range projectProviders {
		names = append(names, detector.Name())
	}
	for _, detector := range repositoryProviders {
		names = append(names, detector.Name())
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

	repo, err := detectRepository(absolute)
	if err != nil {
		return plan.Document{}, fmt.Errorf("detect %s: %w", root, err)
	}
	projects = reconcile.Apply(projects, repo)

	document := plan.NewDocument(projects)
	document.Sort()
	if err := document.Validate(); err != nil {
		return plan.Document{}, fmt.Errorf("detect %s: invalid plan: %w", root, err)
	}
	return document, nil
}

func detectProject(repositoryRoot, path string) (plan.ProjectPlan, error) {
	combined, err := runProviders(projectProviders, provider.Context{RepositoryRoot: repositoryRoot, ProjectPath: path})
	if err != nil {
		return plan.ProjectPlan{}, err
	}

	project := assemble(path, combined)
	if fact, ok := fixtureRoleFact(path); ok {
		project.Facts = append(project.Facts, fact)
	}
	return project, nil
}

func detectRepository(repositoryRoot string) (provider.Result, error) {
	return runProviders(repositoryProviders, provider.Context{RepositoryRoot: repositoryRoot, ProjectPath: "."})
}

func runProviders(detectors []provider.Provider, ctx provider.Context) (provider.Result, error) {
	var combined provider.Result
	for _, detector := range detectors {
		result, err := detector.Detect(ctx)
		if err != nil {
			return provider.Result{}, fmt.Errorf("%s: %w", detector.Name(), err)
		}
		combined.Findings = append(combined.Findings, result.Findings...)
		combined.Ambiguities = append(combined.Ambiguities, result.Ambiguities...)
		combined.Conflicts = append(combined.Conflicts, result.Conflicts...)
	}
	return combined, nil
}
