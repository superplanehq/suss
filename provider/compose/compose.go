// Package compose detects Docker Compose files. It is repository-scoped:
// Detect walks from the repository root for standard compose filenames.
//
// Services become requirements. Environment variable names — declared
// keys and interpolation names from scalar values — are recorded without
// values. When a directory has a default compose file and an override
// file, they are merged with Compose mapping/sequence rules before
// findings are emitted; each finding points at the originating file and
// pointer. Only an active default base file is used; a lone override is
// ignored. `docker compose up -d` is emitted as a preparation candidate
// for each directory that contains an active compose file.
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

var defaultComposeNames = []string{
	"compose.yaml",
	"compose.yml",
	"docker-compose.yaml",
	"docker-compose.yml",
}

var defaultOverrideNames = []string{
	"compose.override.yaml",
	"compose.override.yml",
	"docker-compose.override.yaml",
	"docker-compose.override.yml",
}

var composeNames = func() map[string]struct{} {
	out := make(map[string]struct{}, len(defaultComposeNames)+len(defaultOverrideNames))
	for _, name := range defaultComposeNames {
		out[name] = struct{}{}
	}
	for _, name := range defaultOverrideNames {
		out[name] = struct{}{}
	}
	return out
}()

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
		extracted, projectFiles, err := extractDirectory(ctx, dir, dirFiles)
		if err != nil {
			return provider.Result{}, err
		}
		result.Findings = append(result.Findings, extracted...)
		if len(projectFiles) == 0 {
			continue
		}
		up, err := composeUpCommand(dir, projectFiles)
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

func extractDirectory(ctx provider.Context, dir string, files []string) ([]plan.Finding, []string, error) {
	var findings []plan.Finding
	var projectFiles []string
	for _, rels := range composeProjects(files) {
		got, err := extractProject(ctx, dir, rels)
		if err != nil {
			return nil, nil, err
		}
		findings = append(findings, got...)
		projectFiles = append(projectFiles, rels...)
	}
	return findings, projectFiles, nil
}

func composeProjects(files []string) [][]string {
	base, override := selectComposeFiles(files)
	switch {
	case base != "" && override != "":
		return [][]string{{base, override}}
	case base != "":
		return [][]string{{base}}
	default:
		return nil
	}
}

func extractProject(ctx provider.Context, dir string, rels []string) ([]plan.Finding, error) {
	if len(rels) == 0 {
		return nil, nil
	}
	parsed := make([]composeFile, 0, len(rels))
	for _, rel := range rels {
		file, err := loadCompose(ctx, rel)
		if err != nil {
			return nil, err
		}
		parsed = append(parsed, file)
	}

	file := parsed[0]
	if len(parsed) > 1 {
		merged, err := mergeCompose(parsed[0], parsed[1])
		if err != nil {
			return nil, fmt.Errorf("merge %s and %s: %w", rels[0], rels[1], err)
		}
		file = merged
	}

	var findings []plan.Finding
	for i, rel := range rels {
		if parsed[i].HasInclude {
			findings = append(findings, limitationFinding(rel, "include", "Compose include files are detected but not expanded."))
		}
	}
	for _, name := range sortedKeys(file.Services) {
		svc := file.Services[name]
		findings = append(findings, serviceFinding(dir, name, svc))
		findings = append(findings, environmentFindings(dir, svc.Environment)...)
	}
	for _, item := range file.Interpolations {
		findings = append(findings, interpolationFinding(dir, item))
	}
	return findings, nil
}

func loadCompose(ctx provider.Context, rel string) (composeFile, error) {
	abs := filepath.Join(ctx.RepositoryRoot, filepath.FromSlash(rel))
	contents, err := os.ReadFile(abs)
	if err != nil {
		return composeFile{}, fmt.Errorf("read %s: %w", rel, err)
	}
	file, err := parseCompose(contents, rel)
	if err != nil {
		return composeFile{}, fmt.Errorf("parse %s: %w", rel, err)
	}
	return file, nil
}

func serviceFinding(dir, name string, svc composeService) plan.Finding {
	_, version := splitImage(svc.Image)
	pointer := jsonPointer("services", name)
	source := svc.Source
	if svc.Image != "" {
		pointer = jsonPointer("services", name, "image")
		source = firstNonEmpty(svc.ImageSource, svc.Source)
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

func interpolationFinding(dir string, item locatedVar) plan.Finding {
	return plan.RequirementFinding{
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
				Source:  item.Source,
				Pointer: item.Pointer,
			}},
		},
	}
}

func environmentFindings(dir string, env []envVar) []plan.Finding {
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
					Source:  item.Source,
					Pointer: item.Pointer,
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

func selectComposeFiles(files []string) (base, override string) {
	byName := make(map[string]string, len(files))
	for _, file := range files {
		byName[path.Base(file)] = file
	}
	for _, name := range defaultComposeNames {
		if file, ok := byName[name]; ok {
			base = file
			break
		}
	}
	for _, name := range defaultOverrideNames {
		if file, ok := byName[name]; ok {
			override = file
			break
		}
	}
	return base, override
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
