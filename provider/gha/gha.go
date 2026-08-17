// Package gha detects GitHub Actions workflows. It is repository-scoped:
// Detect reads .github/workflows regardless of Context.ProjectPath.
//
// Known limitations, recorded as project facts when encountered:
//   - reusable workflows (jobs.<id>.uses) are not expanded
//   - local composite actions (steps that uses: ./...) are not expanded
//
// CI run steps that are shell builtins, unix utilities, or VCS plumbing are
// not emitted as commands. Environment names are emitted when the value is a
// secret reference, or a literal default on the workflow (not a job or step).
package gha

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/superplanehq/suss/provider"
)

const providerName = "github-actions"

// Provider detects GitHub Actions workflow files at the repository root.
type Provider struct{}

// Name returns the stable provider identifier.
func (Provider) Name() string {
	return providerName
}

// Detect inspects .github/workflows at the repository root. It ignores
// Context.ProjectPath; callers must invoke it once per repository.
func (Provider) Detect(ctx provider.Context) (provider.Result, error) {
	files, err := workflowFiles(ctx.RepositoryRoot)
	if err != nil {
		return provider.Result{}, err
	}

	var result provider.Result
	for _, rel := range files {
		abs := filepath.Join(ctx.RepositoryRoot, filepath.FromSlash(rel))
		contents, err := os.ReadFile(abs)
		if err != nil {
			return provider.Result{}, fmt.Errorf("read %s: %w", rel, err)
		}
		workflow, err := parseWorkflow(contents)
		if err != nil {
			return provider.Result{}, fmt.Errorf("parse %s: %w", rel, err)
		}
		extracted, err := extract(ctx, rel, workflow)
		if err != nil {
			return provider.Result{}, err
		}
		result.Findings = append(result.Findings, extracted.Findings...)
		result.Ambiguities = append(result.Ambiguities, extracted.Ambiguities...)
		result.Conflicts = append(result.Conflicts, extracted.Conflicts...)
	}
	return result, nil
}

func workflowFiles(root string) ([]string, error) {
	dir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read .github/workflows: %w", err)
	}

	var files []string
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		name := entry.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".yml" && ext != ".yaml" {
			continue
		}
		files = append(files, ".github/workflows/"+name)
	}
	return files, nil
}
