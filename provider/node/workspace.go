package node

import (
	"encoding/json"
	"path"
	"strings"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
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
	parent := parentContext(ctx)
	for parent != nil {
		orchestrators, err := orchestratorsAt(*parent)
		if err != nil {
			return false, err
		}
		if len(orchestrators) > 0 {
			return true, nil
		}
		parent = parentContext(*parent)
	}
	return false, nil
}

func orchestratorsAt(ctx provider.Context) ([]orchestrator, error) {
	manifest, ok, err := readManifest(ctx)
	if err != nil {
		return nil, err
	}
	if !ok {
		manifest = packageManifest{}
	}
	return workspaceOrchestrators(ctx, manifest), nil
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
