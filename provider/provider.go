// Package provider defines the detector plugin interface.
package provider

import (
	"path/filepath"

	"github.com/superplanehq/suss/plan"
)

// Context is the filesystem view a provider receives for one project root.
type Context struct {
	RepositoryRoot string
	ProjectPath    string
}

// ProjectDir is the absolute directory of the project.
func (c Context) ProjectDir() string {
	if c.ProjectPath == "." || c.ProjectPath == "" {
		return c.RepositoryRoot
	}
	return filepath.Join(c.RepositoryRoot, filepath.FromSlash(c.ProjectPath))
}

// SourcePath returns a repository-relative slash path for a file in the project.
func (c Context) SourcePath(name string) string {
	name = filepath.ToSlash(name)
	if c.ProjectPath == "." || c.ProjectPath == "" {
		return name
	}
	return c.ProjectPath + "/" + name
}

// Result is the set of observations one provider emits for one project.
// Ambiguities and conflicts that are closed inside a single provider are
// included here; cross-provider reconciliation is applied after all providers
// have run.
type Result struct {
	Findings    []plan.Finding
	Ambiguities []plan.Ambiguity
	Conflicts   []plan.Conflict
}

// Provider inspects one project root and emits evidence-backed findings.
type Provider interface {
	Name() string
	Detect(ctx Context) (Result, error)
}
