// Package compose detects Docker Compose files. It is repository-scoped:
// Detect walks from the repository root for standard compose filenames.
//
// Services become requirements. Environment variable names are recorded
// without values. `docker compose up -d` is emitted as a preparation
// candidate for each directory that contains a compose file.
//
// Known limitation: compose `include:` is detected but not expanded.
package compose

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

const providerName = "compose"

var composeNames = map[string]struct{}{
	"compose.yaml":                 {},
	"compose.yml":                  {},
	"docker-compose.yaml":          {},
	"docker-compose.yml":           {},
	"compose.override.yaml":        {},
	"compose.override.yml":         {},
	"docker-compose.override.yaml": {},
	"docker-compose.override.yml":  {},
}

var skippedDirectories = map[string]struct{}{
	"node_modules": {},
	"vendor":       {},
	"_build":       {},
	"deps":         {},
	"dist":         {},
	"target":       {},
}

// Provider detects Docker Compose files at the repository root and below.
type Provider struct{}

// Name returns the stable provider identifier.
func (Provider) Name() string {
	return providerName
}

// Detect inspects the repository for compose files. It ignores
// Context.ProjectPath; callers must invoke it once per repository.
func (Provider) Detect(ctx provider.Context) (provider.Result, error) {
	files, err := findComposeFiles(ctx.RepositoryRoot)
	if err != nil {
		return provider.Result{}, err
	}
	if len(files) == 0 {
		return provider.Result{}, nil
	}

	var result provider.Result
	byDir := groupByDirectory(files)
	for _, dir := range sortedKeys(byDir) {
		dirFiles := byDir[dir]
		result.Findings = append(result.Findings, dockerToolFinding(dir, dirFiles))
		for _, rel := range dirFiles {
			extracted, err := extractFile(ctx, dir, rel)
			if err != nil {
				return provider.Result{}, err
			}
			result.Findings = append(result.Findings, extracted...)
		}
		up, err := composeUpCommand(dir, dirFiles)
		if err != nil {
			return provider.Result{}, err
		}
		result.Findings = append(result.Findings, plan.CommandFinding{
			ProjectPath: dir,
			Detector:    providerName,
			Command:     up,
		})
	}
	return result, nil
}

func extractFile(ctx provider.Context, dir, rel string) ([]plan.Finding, error) {
	abs := filepath.Join(ctx.RepositoryRoot, filepath.FromSlash(rel))
	contents, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", rel, err)
	}
	file, err := parseCompose(contents)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", rel, err)
	}

	var findings []plan.Finding
	if file.HasInclude {
		findings = append(findings, limitationFinding(rel, "include", "Compose include files are detected but not expanded."))
	}
	for _, name := range sortedKeys(file.Services) {
		svc := file.Services[name]
		findings = append(findings, serviceFinding(dir, rel, name, svc))
		findings = append(findings, environmentFindings(dir, rel, name, svc.Environment)...)
		findings = append(findings, interpolationFindings(dir, rel, jsonPointer("services", name, "image"), svc.ImageVars)...)
		findings = append(findings, interpolationFindings(dir, rel, jsonPointer("services", name, "environment"), svc.ValueVars)...)
	}
	return findings, nil
}

func serviceFinding(dir, source, name string, svc composeService) plan.Finding {
	_, version := splitImage(svc.Image)
	pointer := jsonPointer("services", name)
	if svc.Image != "" {
		pointer = jsonPointer("services", name, "image")
	}
	return plan.RequirementFinding{
		ProjectPath: dir,
		Detector:    providerName,
		Requirement: plan.Requirement{
			Kind:       plan.RequirementService,
			Name:       name,
			Version:    version,
			Confidence: plan.ConfidenceHigh,
			Evidence: []plan.Evidence{{
				Kind:    plan.EvidenceDeclaration,
				Source:  source,
				Pointer: pointer,
			}},
		},
	}
}

func interpolationFindings(dir, source, pointer string, env []envVar) []plan.Finding {
	findings := make([]plan.Finding, 0, len(env))
	for _, item := range env {
		findings = append(findings, plan.RequirementFinding{
			ProjectPath: dir,
			Detector:    providerName,
			Requirement: plan.Requirement{
				Kind:       plan.RequirementEnvironment,
				Name:       item.Name,
				IsRequired: boolPtr(true),
				HasDefault: boolPtr(item.HasDefault),
				Confidence: plan.ConfidenceHigh,
				Evidence: []plan.Evidence{{
					Kind:    plan.EvidenceDeclaration,
					Source:  source,
					Pointer: pointer,
				}},
			},
		})
	}
	return findings
}

func environmentFindings(dir, source, service string, env []envVar) []plan.Finding {
	findings := make([]plan.Finding, 0, len(env))
	for _, item := range env {
		findings = append(findings, plan.RequirementFinding{
			ProjectPath: dir,
			Detector:    providerName,
			Requirement: plan.Requirement{
				Kind:       plan.RequirementEnvironment,
				Name:       item.Name,
				IsRequired: boolPtr(true),
				HasDefault: boolPtr(item.HasDefault),
				Confidence: plan.ConfidenceHigh,
				Evidence: []plan.Evidence{{
					Kind:    plan.EvidenceDeclaration,
					Source:  source,
					Pointer: jsonPointer("services", service, "environment", item.Name),
				}},
			},
		})
	}
	return findings
}

func composeUpCommand(dir string, files []string) (plan.Command, error) {
	source := files[0]
	id, err := plan.NewCommandID(plan.CommandIdentity{
		ProjectPath: dir,
		Provider:    providerName,
		Source:      source,
		Pointer:     "/#up",
	})
	if err != nil {
		return plan.Command{}, err
	}

	evidence := make([]plan.Evidence, 0, len(files)+1)
	for _, file := range files {
		evidence = append(evidence, plan.Evidence{
			Kind:   plan.EvidenceFile,
			Source: file,
		})
	}
	evidence = append(evidence, plan.Evidence{
		Kind:        plan.EvidenceConvention,
		Source:      "compose",
		Pointer:     "up",
		Description: "Docker Compose services are started with docker compose up -d.",
	})

	return plan.Command{
		ID:              id,
		Name:            "start services",
		Run:             stringPtr("docker compose up -d"),
		Directory:       dir,
		Scope:           plan.ScopeProject,
		Origin:          plan.CommandInferred,
		Confidence:      plan.ConfidenceHigh,
		Evidence:        evidence,
		Interpretations: []plan.Interpretation{},
		Variants:        []plan.CommandVariant{},
	}, nil
}

func dockerToolFinding(dir string, files []string) plan.Finding {
	evidence := make([]plan.Evidence, 0, len(files))
	for _, file := range files {
		evidence = append(evidence, plan.Evidence{
			Kind:   plan.EvidenceFile,
			Source: file,
		})
	}
	return plan.RequirementFinding{
		ProjectPath: dir,
		Detector:    providerName,
		Requirement: plan.Requirement{
			Kind:       plan.RequirementTool,
			Name:       "docker",
			Confidence: plan.ConfidenceHigh,
			Evidence:   evidence,
		},
	}
}

func limitationFinding(source, value, description string) plan.Finding {
	return plan.PropertyFinding{
		ProjectPath: directoryOf(source),
		Detector:    providerName,
		Property: plan.Property{
			Kind:       plan.PropertyFact,
			Name:       "provider.compose.limitation",
			Value:      value,
			Confidence: plan.ConfidenceHigh,
			Evidence: []plan.Evidence{{
				Kind:        plan.EvidenceFile,
				Source:      source,
				Description: description,
			}},
		},
	}
}

func findComposeFiles(root string) ([]string, error) {
	var files []string
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
		if _, ok := composeNames[entry.Name()]; !ok {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "../") || rel == ".." {
			return nil
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk compose files: %w", err)
	}
	slices.Sort(files)
	return files, nil
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

func groupByDirectory(files []string) map[string][]string {
	out := make(map[string][]string)
	for _, file := range files {
		dir := directoryOf(file)
		out[dir] = append(out[dir], file)
	}
	return out
}

func directoryOf(file string) string {
	dir := path.Dir(file)
	if dir == "." || dir == "" {
		return "."
	}
	return dir
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func splitImage(image string) (string, string) {
	image = strings.TrimSpace(image)
	if image == "" {
		return "", ""
	}
	image = strings.TrimPrefix(image, "docker://")
	name, digest, _ := strings.Cut(image, "@")
	slash := strings.LastIndex(name, "/")
	repo := name
	if slash >= 0 {
		repo = name[slash+1:]
	}
	repoName, tag, found := strings.Cut(repo, ":")
	if !found {
		return path.Base(repoName), digest
	}
	if digest != "" {
		return repoName, digest
	}
	if strings.Contains(tag, "$") {
		if def := interpolationDefault(tag); def != "" && !strings.Contains(def, "$") {
			return repoName, def
		}
		return repoName, ""
	}
	return repoName, tag
}

func interpolationDefault(value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "${") || !strings.HasSuffix(value, "}") {
		return ""
	}
	inner := value[2 : len(value)-1]
	for _, sep := range []string{":-", "-", ":="} {
		_, def, found := strings.Cut(inner, sep)
		if found {
			return def
		}
	}
	return ""
}

func jsonPointer(parts ...string) string {
	var b strings.Builder
	for _, part := range parts {
		b.WriteByte('/')
		b.WriteString(escapePointer(part))
	}
	return b.String()
}

func escapePointer(value string) string {
	value = strings.ReplaceAll(value, "~", "~0")
	return strings.ReplaceAll(value, "/", "~1")
}

func stringPtr(value string) *string {
	return &value
}

func boolPtr(value bool) *bool {
	return &value
}
