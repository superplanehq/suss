// Package suss inspects a repository and reports how to set it up, build it,
// test it, and run it. Detection is static: it does not execute commands or
// install dependencies.
package suss

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/superplanehq/suss/plan"
)

// Detect inspects root and returns a schema-versioned plan document.
// Milestone 1 only discovers project roots; providers fill in the rest later.
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
		projects = append(projects, plan.NewProjectPlan(path))
	}

	document := plan.NewDocument(projects)
	document.Sort()
	if err := document.Validate(); err != nil {
		return plan.Document{}, fmt.Errorf("detect %s: invalid plan: %w", root, err)
	}
	return document, nil
}
