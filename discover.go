package suss

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/superplanehq/suss/plan"
)

var projectManifests = map[string]struct{}{
	"package.json":        {},
	"go.mod":              {},
	"mix.exs":             {},
	"Gemfile":             {},
	"composer.json":       {},
	"pom.xml":             {},
	"build.gradle":        {},
	"build.gradle.kts":    {},
	"settings.gradle":     {},
	"settings.gradle.kts": {},
	"GNUmakefile":         {},
	"Makefile":            {},
	"makefile":            {},
	".env.example":        {},
	".env.sample":         {},
	".env.template":       {},
}

var gradleSettingsFiles = map[string]struct{}{
	"settings.gradle":     {},
	"settings.gradle.kts": {},
}

var skippedDirectories = map[string]struct{}{
	"node_modules": {},
	"vendor":       {},
	"vendor-bin":   {},
	"_build":       {},
	"deps":         {},
	"dist":         {},
	"target":       {},
	"tmp":          {},
	"buildSrc":     {},
}

func findProjectRoots(root string) ([]string, error) {
	found := make(map[string]map[string]struct{})
	settings := make(map[string]struct{})

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
		if found[relative] == nil {
			found[relative] = make(map[string]struct{})
		}
		found[relative][entry.Name()] = struct{}{}
		if _, ok := gradleSettingsFiles[entry.Name()]; ok {
			settings[relative] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	includes, err := loadGradleIncludes(root, settings)
	if err != nil {
		return nil, err
	}

	paths := make([]string, 0, len(found))
	for path, files := range found {
		if isDeclaredGradleMember(path, files, settings, includes) {
			continue
		}
		paths = append(paths, path)
	}
	slices.Sort(paths)
	return paths, nil
}

func loadGradleIncludes(root string, settings map[string]struct{}) (map[string][]string, error) {
	includes := make(map[string][]string, len(settings))
	for relative := range settings {
		dir := root
		if relative != "." {
			dir = filepath.Join(root, filepath.FromSlash(relative))
		}
		var contents strings.Builder
		for _, name := range []string{"settings.gradle.kts", "settings.gradle"} {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err == nil {
				contents.Write(data)
				contents.WriteByte('\n')
				continue
			}
			if !os.IsNotExist(err) {
				return nil, err
			}
		}
		includes[relative] = parseGradleIncludes(contents.String())
	}
	return includes, nil
}

var (
	gradleIncludeCall = regexp.MustCompile(`(?m)(?:^|[^\w])include\s*\(([^)]*)\)`)
	gradleIncludeBare = regexp.MustCompile(`(?m)(?:^|[^\w])include\s+([^;\n]+)`)
	gradleQuoted      = regexp.MustCompile(`["']([^"']+)["']`)
)

func parseGradleIncludes(contents string) []string {
	var paths []string
	seen := make(map[string]struct{})
	add := func(raw string) {
		raw = strings.TrimSpace(raw)
		raw = strings.Trim(raw, ":")
		raw = strings.ReplaceAll(raw, ":", "/")
		if raw == "" {
			return
		}
		if _, ok := seen[raw]; ok {
			return
		}
		seen[raw] = struct{}{}
		paths = append(paths, raw)
	}
	for _, match := range gradleIncludeCall.FindAllStringSubmatch(contents, -1) {
		for _, quoted := range gradleQuoted.FindAllStringSubmatch(match[1], -1) {
			add(quoted[1])
		}
	}
	for _, match := range gradleIncludeBare.FindAllStringSubmatch(contents, -1) {
		for _, quoted := range gradleQuoted.FindAllStringSubmatch(match[1], -1) {
			add(quoted[1])
		}
	}
	return paths
}

func isDeclaredGradleMember(path string, files map[string]struct{}, settings map[string]struct{}, includes map[string][]string) bool {
	if _, ok := settings[path]; ok {
		return false
	}
	if !onlyGradleBuildManifests(files) {
		return false
	}
	dir := path
	for dir != "." && dir != "" {
		parent := parentProjectPath(dir)
		if parent == dir {
			break
		}
		if gradleIncludeContains(includes[parent], relativeToParent(parent, path)) {
			return true
		}
		dir = parent
	}
	return false
}

func onlyGradleBuildManifests(files map[string]struct{}) bool {
	if len(files) == 0 {
		return false
	}
	for name := range files {
		switch name {
		case "build.gradle", "build.gradle.kts":
		default:
			return false
		}
	}
	return true
}

func gradleIncludeContains(listed []string, relative string) bool {
	if relative == "" {
		return false
	}
	return slices.Contains(listed, relative)
}

func relativeToParent(parent, path string) string {
	if parent == "." {
		return path
	}
	prefix := parent + "/"
	if strings.HasPrefix(path, prefix) {
		return path[len(prefix):]
	}
	return ""
}

func parentProjectPath(projectPath string) string {
	projectPath = strings.Trim(projectPath, "/")
	index := strings.LastIndex(projectPath, "/")
	if index <= 0 {
		return "."
	}
	return projectPath[:index]
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
