package suss

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/superplanehq/suss/plan"
)

var projectManifests = map[string]struct{}{
	"package.json":   {},
	"go.mod":         {},
	"Cargo.toml":     {},
	"mix.exs":        {},
	"Gemfile":        {},
	"composer.json":  {},
	"pyproject.toml": {},
	"setup.py":       {},
	"Pipfile":        {},
	"GNUmakefile":    {},
	"Makefile":       {},
	"makefile":       {},
	".env.example":   {},
	".env.sample":    {},
	".env.template":  {},
}

var skippedDirectories = map[string]struct{}{
	"node_modules":  {},
	"vendor":        {},
	"vendor-bin":    {},
	"_build":        {},
	"deps":          {},
	"dist":          {},
	"target":        {},
	"tmp":           {},
	"venv":          {},
	"virtualenv":    {},
	"env":           {},
	"site-packages": {},
	"__pycache__":   {},
	"htmlcov":       {},
}

func findProjectRoots(root string) ([]string, error) {
	found := make(map[string]struct{})

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path == root {
				return nil
			}
			if shouldSkipDirectory(entry) {
				return filepath.SkipDir
			}
			return nil
		}
		if _, ok := projectManifests[entry.Name()]; !ok {
			return nil
		}

		relative, err := relativeProjectPath(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		found[relative] = struct{}{}
		return nil
	})
	if err != nil {
		return nil, err
	}

	paths := make([]string, 0, len(found))
	for path := range found {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	return paths, nil
}

func shouldSkipDirectory(entry fs.DirEntry) bool {
	name := entry.Name()
	if strings.HasPrefix(name, ".") {
		return true
	}
	if _, skipped := skippedDirectories[name]; skipped {
		return true
	}
	return entry.Type()&os.ModeSymlink != 0
}

// Fixture-like roots remain in the versioned document. Path evidence is
// attached as project.role=fixture so renderers and other consumers can filter
// them without losing visibility in the underlying plan.
// testdata, fixtures, and __fixtures__ are high-confidence; examples is
// medium because real packages sometimes live there.
var fixtureSegments = map[string]plan.Confidence{
	"testdata":     plan.ConfidenceHigh,
	"fixtures":     plan.ConfidenceHigh,
	"__fixtures__": plan.ConfidenceHigh,
	"examples":     plan.ConfidenceMedium,
}

func fixtureRoleFact(projectPath string) (plan.ProjectFact, bool) {
	segment, confidence, ok := fixtureSegment(projectPath)
	if !ok {
		return plan.ProjectFact{}, false
	}
	return plan.ProjectFact{
		Name:       "project.role",
		Value:      "fixture",
		Confidence: confidence,
		Evidence: []plan.Evidence{{
			Kind:        plan.EvidenceFile,
			Source:      projectPath,
			Description: fmt.Sprintf("The project root sits under the %s path segment.", segment),
		}},
	}, true
}

func fixtureSegment(projectPath string) (string, plan.Confidence, bool) {
	if projectPath == "." || projectPath == "" {
		return "", "", false
	}

	bestSegment := ""
	var bestConfidence plan.Confidence
	found := false
	for _, segment := range strings.Split(projectPath, "/") {
		confidence, ok := fixtureSegments[segment]
		if !ok {
			continue
		}
		if !found || confidenceRank(confidence) > confidenceRank(bestConfidence) {
			bestSegment = segment
			bestConfidence = confidence
			found = true
		}
	}
	return bestSegment, bestConfidence, found
}

func confidenceRank(confidence plan.Confidence) int {
	switch confidence {
	case plan.ConfidenceHigh:
		return 3
	case plan.ConfidenceMedium:
		return 2
	case plan.ConfidenceLow:
		return 1
	default:
		return 0
	}
}

func relativeProjectPath(root, dir string) (string, error) {
	relative, err := filepath.Rel(root, dir)
	if err != nil {
		return "", err
	}
	relative = filepath.ToSlash(relative)
	if relative == "." || relative == "" {
		return ".", nil
	}
	if strings.HasPrefix(relative, "../") || relative == ".." {
		return "", fs.ErrInvalid
	}
	return relative, nil
}
