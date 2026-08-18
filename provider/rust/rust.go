// Package rust detects Cargo packages from Cargo.toml and related files.
package rust

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

const providerName = "rust"

// Provider detects Rust projects from Cargo.toml and related files.
type Provider struct{}

// Name returns the stable provider identifier.
func (Provider) Name() string {
	return providerName
}

// Detect inspects one project root. It returns an empty result when the
// directory has no Cargo.toml.
func (Provider) Detect(ctx provider.Context) (provider.Result, error) {
	manifest, ok, err := readCargoTOML(ctx)
	if err != nil {
		return provider.Result{}, err
	}
	if !ok {
		return provider.Result{}, nil
	}

	var result provider.Result
	result.Findings = append(result.Findings, projectFindings(ctx, manifest)...)
	runtimes, conflicts, err := runtimeFindings(ctx, manifest)
	if err != nil {
		return provider.Result{}, err
	}
	result.Findings = append(result.Findings, runtimes...)
	result.Conflicts = append(result.Conflicts, conflicts...)
	result.Findings = append(result.Findings, toolFindings(ctx)...)

	commands, err := inferredCommands(ctx)
	if err != nil {
		return provider.Result{}, err
	}
	result.Findings = append(result.Findings, commands...)
	return result, nil
}

func readCargoTOML(ctx provider.Context) (cargoManifest, bool, error) {
	path := filepath.Join(ctx.ProjectDir(), "Cargo.toml")
	contents, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cargoManifest{}, false, nil
	}
	if err != nil {
		return cargoManifest{}, false, fmt.Errorf("read Cargo.toml: %w", err)
	}
	return parseCargoTOML(string(contents)), true, nil
}

func projectFindings(ctx provider.Context, manifest cargoManifest) []plan.Finding {
	source := ctx.SourcePath("Cargo.toml")
	language := plan.Evidence{Kind: plan.EvidenceDeclaration, Source: source}
	switch {
	case manifest.Name != "":
		language.Pointer = "/package/name"
	case manifest.HasPackage:
		language.Pointer = "/package"
	case manifest.HasWorkspace:
		language.Pointer = "/workspace"
	}

	findings := []plan.Finding{
		propertyFinding(ctx, plan.PropertyLanguage, "rust", "", []plan.Evidence{language}),
		propertyFinding(ctx, plan.PropertyPackageManager, "cargo", "", cargoManagerEvidence(ctx)),
	}
	if manifest.HasWorkspace {
		findings = append(findings, propertyFinding(ctx, plan.PropertyFact, "workspace.orchestrator", "cargo", []plan.Evidence{{
			Kind:    plan.EvidenceConfiguration,
			Source:  source,
			Pointer: "/workspace",
		}}))
	}
	for _, framework := range packageFrameworks(manifest.Dependencies) {
		pointer := framework.Key
		if pointer == "" {
			pointer = framework.Name
		}
		findings = append(findings, propertyFinding(ctx, plan.PropertyFramework, framework.Name, "", []plan.Evidence{{
			Kind:        plan.EvidenceDeclaration,
			Source:      source,
			Pointer:     "/dependencies/" + pointerToken(pointer),
			Description: "The Cargo dependency list includes " + framework.Name + ".",
		}}))
	}
	return findings
}

func cargoManagerEvidence(ctx provider.Context) []plan.Evidence {
	evidence := []plan.Evidence{{
		Kind:   plan.EvidenceDeclaration,
		Source: ctx.SourcePath("Cargo.toml"),
	}}
	if fileExists(ctx.ProjectDir(), "Cargo.lock") {
		evidence = append(evidence, plan.Evidence{
			Kind:   plan.EvidenceFile,
			Source: ctx.SourcePath("Cargo.lock"),
		})
	}
	return evidence
}

func propertyFinding(ctx provider.Context, kind plan.PropertyKind, name, value string, evidence []plan.Evidence) plan.Finding {
	return plan.PropertyFinding{
		ProjectPath: ctx.ProjectPath,
		Detector:    providerName,
		Property: plan.Property{
			Kind:       kind,
			Name:       name,
			Value:      value,
			Confidence: plan.ConfidenceHigh,
			Evidence:   evidence,
		},
	}
}

func fileExists(dir, name string) bool {
	info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name)))
	return err == nil && !info.IsDir()
}

func stringPtr(value string) *string {
	return &value
}

func pointerToken(value string) string {
	value = strings.ReplaceAll(value, "~", "~0")
	return strings.ReplaceAll(value, "/", "~1")
}

func walkSkipDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "vendor", "testdata", "node_modules", "_build", "deps", "dist", "target", "tmp":
		return true
	default:
		return false
	}
}

func findTestFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path == root {
				return nil
			}
			if walkSkipDir(entry.Name()) || entry.Type()&os.ModeSymlink != 0 {
				return filepath.SkipDir
			}
			if fileExists(path, "Cargo.toml") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".rs") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		ok, err := isRustTestFile(path, rel, entry.Name())
		if err != nil {
			return err
		}
		if ok {
			files = append(files, rel)
		}
		return nil
	})
	return files, err
}

func isRustTestFile(abs, rel, name string) (bool, error) {
	if strings.HasSuffix(name, "_test.rs") {
		return true, nil
	}
	if strings.HasPrefix(rel, "tests/") || strings.Contains(rel, "/tests/") {
		return true, nil
	}
	contents, err := os.ReadFile(abs)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", rel, err)
	}
	return rustSourceHasTest(string(contents)), nil
}

var rustTestAttribute = regexp.MustCompile(`#\[\s*(?:(?:[A-Za-z_][A-Za-z0-9_]*::)*test|rstest)(?:\s*(?:\(|]))`)

func rustSourceHasTest(contents string) bool {
	return rustTestAttribute.MatchString(contents)
}
