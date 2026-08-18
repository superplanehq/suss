// Package php detects Composer-managed PHP projects from composer.json and
// related structured files. Detection is static and never evaluates PHP.
package php

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

const providerName = "php"

// Provider detects one Composer-managed PHP project.
type Provider struct{}

// Name returns the stable provider identifier.
func (Provider) Name() string {
	return providerName
}

// Detect inspects one project root. It returns no findings without composer.json.
func (Provider) Detect(ctx provider.Context) (provider.Result, error) {
	manifest, ok, err := readComposer(ctx)
	if err != nil || !ok {
		return provider.Result{}, err
	}

	result := provider.Result{Findings: projectFindings(ctx, manifest)}
	runtimes, conflicts, err := runtimeFindings(ctx, manifest)
	if err != nil {
		return provider.Result{}, err
	}
	result.Findings = append(result.Findings, runtimes...)
	result.Conflicts = append(result.Conflicts, conflicts...)
	result.Findings = append(result.Findings, configuredToolFindings(ctx, manifest)...)

	commands, err := commandFindings(ctx, manifest)
	if err != nil {
		return provider.Result{}, err
	}
	result.Findings = append(result.Findings, commands...)
	return result, nil
}

func readComposer(ctx provider.Context) (composerManifest, bool, error) {
	contents, err := os.ReadFile(filepath.Join(ctx.ProjectDir(), "composer.json"))
	if os.IsNotExist(err) {
		return composerManifest{}, false, nil
	}
	if err != nil {
		return composerManifest{}, false, fmt.Errorf("read composer.json: %w", err)
	}

	var manifest composerManifest
	if err := json.Unmarshal(contents, &manifest); err != nil {
		return composerManifest{}, false, fmt.Errorf("parse %s: %w", ctx.SourcePath("composer.json"), err)
	}
	return manifest, true, nil
}

func projectFindings(ctx provider.Context, manifest composerManifest) []plan.Finding {
	source := ctx.SourcePath("composer.json")
	declaration := []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: source}}
	findings := []plan.Finding{
		propertyFinding(ctx, plan.PropertyLanguage, "php", "", declaration),
	}

	bundlerEvidence := append([]plan.Evidence{}, declaration...)
	if fileExists(ctx.ProjectDir(), "composer.lock") {
		bundlerEvidence = append(bundlerEvidence, plan.Evidence{
			Kind:   plan.EvidenceDeclaration,
			Source: ctx.SourcePath("composer.lock"),
		})
	}
	findings = append(findings, propertyFinding(ctx, plan.PropertyPackageManager, "composer", "", bundlerEvidence))

	if name, pointer, ok := frameworkDeclaration(manifest); ok {
		findings = append(findings, propertyFinding(ctx, plan.PropertyFramework, name, "", []plan.Evidence{{
			Kind:        plan.EvidenceDeclaration,
			Source:      source,
			Pointer:     pointer,
			Description: "The composer.json require list declares " + name + ".",
		}}))
	}
	return findings
}

func frameworkDeclaration(manifest composerManifest) (name, pointer string, ok bool) {
	if pointer, ok = packagePointer(manifest, "laravel/framework"); ok {
		return "laravel", pointer, true
	}
	if pointer, ok = packagePointer(manifest, "symfony/framework-bundle"); ok {
		return "symfony", pointer, true
	}
	if pointer, ok = packagePointer(manifest, "symfony/symfony"); ok {
		return "symfony", pointer, true
	}
	return "", "", false
}

func propertyFinding(ctx provider.Context, kind plan.PropertyKind, name, version string, evidence []plan.Evidence) plan.Finding {
	return plan.PropertyFinding{
		ProjectPath: ctx.ProjectPath,
		Detector:    providerName,
		Property: plan.Property{
			Kind:       kind,
			Name:       name,
			Version:    version,
			Confidence: plan.ConfidenceHigh,
			Evidence:   evidence,
		},
	}
}

func factFinding(ctx provider.Context, name, value string, evidence []plan.Evidence) plan.Finding {
	return plan.PropertyFinding{
		ProjectPath: ctx.ProjectPath,
		Detector:    providerName,
		Property: plan.Property{
			Kind:       plan.PropertyFact,
			Name:       name,
			Value:      value,
			Confidence: plan.ConfidenceHigh,
			Evidence:   evidence,
		},
	}
}

func stringPtr(value string) *string {
	return &value
}

func pointerToken(value string) string {
	value = strings.ReplaceAll(value, "~", "~0")
	return strings.ReplaceAll(value, "/", "~1")
}

func fileExists(dir, name string) bool {
	info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name)))
	return err == nil && !info.IsDir()
}
