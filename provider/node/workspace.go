package node

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
	"gopkg.in/yaml.v3"
)

func workspaceFindings(ctx provider.Context, manifest packageManifest) []plan.Finding {
	var findings []plan.Finding
	for _, orchestrator := range workspaceOrchestrators(ctx, manifest) {
		findings = append(findings, plan.PropertyFinding{
			ProjectPath: ctx.ProjectPath,
			Detector:    providerName,
			Property: plan.Property{
				Kind:       plan.PropertyFact,
				Name:       "workspace.orchestrator",
				Value:      orchestrator.value,
				Confidence: plan.ConfidenceHigh,
				Evidence:   orchestrator.evidence,
			},
		})
	}
	return findings
}

type orchestrator struct {
	value    string
	evidence []plan.Evidence
}

func workspaceOrchestrators(ctx provider.Context, manifest packageManifest) []orchestrator {
	var found []orchestrator
	if fileExists(ctx.ProjectDir(), "pnpm-workspace.yaml") {
		found = append(found, orchestrator{
			value: "pnpm",
			evidence: []plan.Evidence{{
				Kind:   plan.EvidenceConfiguration,
				Source: ctx.SourcePath("pnpm-workspace.yaml"),
			}},
		})
	}
	if hasWorkspaces(manifest) {
		manager := "npm"
		if declared, _, err := parsePackageManagerField(strings.TrimSpace(manifest.PackageManager)); err == nil && declared != "" {
			manager = declared
		} else if lock := localLockfileManager(ctx); lock != "" {
			manager = lock
		}
		found = append(found, orchestrator{
			value: manager,
			evidence: []plan.Evidence{{
				Kind:    plan.EvidenceDeclaration,
				Source:  ctx.SourcePath("package.json"),
				Pointer: "/workspaces",
			}},
		})
	}
	if fileExists(ctx.ProjectDir(), "turbo.json") {
		found = append(found, orchestrator{
			value: "turbo",
			evidence: []plan.Evidence{{
				Kind:   plan.EvidenceConfiguration,
				Source: ctx.SourcePath("turbo.json"),
			}},
		})
	}
	if fileExists(ctx.ProjectDir(), "nx.json") {
		found = append(found, orchestrator{
			value: "nx",
			evidence: []plan.Evidence{{
				Kind:   plan.EvidenceConfiguration,
				Source: ctx.SourcePath("nx.json"),
			}},
		})
	}
	return found
}

func hasWorkspaces(manifest packageManifest) bool {
	if !hasJSONValue(manifest.Workspaces) {
		return false
	}
	var packages []string
	if err := json.Unmarshal(manifest.Workspaces, &packages); err == nil {
		return len(packages) > 0
	}
	var object struct {
		Packages []string `json:"packages"`
	}
	if err := json.Unmarshal(manifest.Workspaces, &object); err == nil {
		return len(object.Packages) > 0
	}
	return true
}

func localLockfileManager(ctx provider.Context) string {
	for _, lockfile := range lockfiles {
		if fileExists(ctx.ProjectDir(), lockfile.file) {
			return lockfile.manager
		}
	}
	return ""
}

func isWorkspaceMember(ctx provider.Context) (bool, error) {
	_, ok, err := workspaceAncestor(ctx)
	return ok, err
}

func workspaceAncestor(ctx provider.Context) (*provider.Context, bool, error) {
	parent := parentContext(ctx)
	for parent != nil {
		globs, err := workspacePackageGlobs(*parent)
		if err != nil {
			return nil, false, err
		}
		rel := relativeProjectPath(parent.ProjectPath, ctx.ProjectPath)
		if matchesWorkspaceGlobs(globs, rel) {
			return parent, true, nil
		}
		parent = parentContext(*parent)
	}
	return nil, false, nil
}

func workspacePackageGlobs(ctx provider.Context) ([]string, error) {
	var globs []string
	manifest, ok, err := readManifest(ctx)
	if err != nil {
		return nil, err
	}
	if ok {
		globs = append(globs, workspacesFromManifest(manifest)...)
	}
	pnpm, err := pnpmWorkspacePackages(ctx)
	if err != nil {
		return nil, err
	}
	return append(globs, pnpm...), nil
}

func workspacesFromManifest(manifest packageManifest) []string {
	if !hasJSONValue(manifest.Workspaces) {
		return nil
	}
	var packages []string
	if err := json.Unmarshal(manifest.Workspaces, &packages); err == nil {
		return packages
	}
	var object struct {
		Packages []string `json:"packages"`
	}
	if err := json.Unmarshal(manifest.Workspaces, &object); err == nil {
		return object.Packages
	}
	return nil
}

func pnpmWorkspacePackages(ctx provider.Context) ([]string, error) {
	abs := filepath.Join(ctx.ProjectDir(), "pnpm-workspace.yaml")
	contents, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var parsed struct {
		Packages []string `yaml:"packages"`
	}
	if err := yaml.Unmarshal(contents, &parsed); err != nil {
		return nil, err
	}
	return parsed.Packages, nil
}

func relativeProjectPath(ancestor, project string) string {
	if ancestor == "." || ancestor == "" {
		return project
	}
	return strings.TrimPrefix(project, ancestor+"/")
}

func matchesWorkspaceGlobs(globs []string, rel string) bool {
	rel = strings.TrimPrefix(rel, "./")
	included := false
	for _, glob := range globs {
		glob = strings.TrimSpace(glob)
		if glob == "" {
			continue
		}
		if strings.HasPrefix(glob, "!") {
			if matchWorkspaceGlob(strings.TrimPrefix(glob, "!"), rel) {
				return false
			}
			continue
		}
		if matchWorkspaceGlob(glob, rel) {
			included = true
		}
	}
	return included
}

func matchWorkspaceGlob(glob, rel string) bool {
	glob = strings.TrimSuffix(glob, "/")
	if strings.Contains(glob, "**") {
		prefix := strings.TrimSuffix(strings.TrimSuffix(glob, "**"), "/")
		if prefix == "" {
			return true
		}
		return rel == prefix || strings.HasPrefix(rel, prefix+"/")
	}
	ok, err := path.Match(glob, rel)
	return err == nil && ok
}

func parentContext(ctx provider.Context) *provider.Context {
	if ctx.ProjectPath == "." || ctx.ProjectPath == "" {
		return nil
	}
	parent := path.Dir(ctx.ProjectPath)
	if parent == "/" || parent == "." {
		parent = "."
	}
	return &provider.Context{RepositoryRoot: ctx.RepositoryRoot, ProjectPath: parent}
}
