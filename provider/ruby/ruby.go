// Package ruby detects Ruby and Ruby on Rails projects from Gemfile and
// related structured files. Detection is static and never evaluates Ruby.
package ruby

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

const providerName = "ruby"

// Provider detects one Bundler-managed Ruby project.
type Provider struct{}

// Name returns the stable provider identifier.
func (Provider) Name() string {
	return providerName
}

// Detect inspects one project root. It returns no findings without a Gemfile.
func (Provider) Detect(ctx provider.Context) (provider.Result, error) {
	contents, ok, err := readGemfile(ctx)
	if err != nil || !ok {
		return provider.Result{}, err
	}

	manifest := parseGemfile(contents)
	lock, err := readGemfileLock(ctx)
	if err != nil {
		return provider.Result{}, err
	}

	result := provider.Result{Findings: projectFindings(ctx, manifest, lock)}
	runtimes, conflicts, err := runtimeFindings(ctx, manifest.RubyVersion)
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

func readGemfile(ctx provider.Context) (string, bool, error) {
	contents, err := os.ReadFile(filepath.Join(ctx.ProjectDir(), "Gemfile"))
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read Gemfile: %w", err)
	}
	return string(contents), true, nil
}

func readGemfileLock(ctx provider.Context) (gemfileLock, error) {
	contents, err := os.ReadFile(filepath.Join(ctx.ProjectDir(), "Gemfile.lock"))
	if os.IsNotExist(err) {
		return gemfileLock{}, nil
	}
	if err != nil {
		return gemfileLock{}, fmt.Errorf("read Gemfile.lock: %w", err)
	}
	return parseGemfileLock(string(contents)), nil
}

func projectFindings(ctx provider.Context, manifest gemfile, lock gemfileLock) []plan.Finding {
	gemfileSource := ctx.SourcePath("Gemfile")
	declaration := []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: gemfileSource}}
	findings := []plan.Finding{
		propertyFinding(ctx, plan.PropertyLanguage, "ruby", "", declaration),
	}

	bundlerEvidence := append([]plan.Evidence{}, declaration...)
	if lock.BundlerVersion != "" {
		bundlerEvidence = append(bundlerEvidence, plan.Evidence{
			Kind:    plan.EvidenceDeclaration,
			Source:  ctx.SourcePath("Gemfile.lock"),
			Pointer: "/BUNDLED WITH",
		})
	}
	findings = append(findings, propertyFinding(ctx, plan.PropertyPackageManager, "bundler", lock.BundlerVersion, bundlerEvidence))

	if rails, ok := manifest.Gems["rails"]; ok {
		findings = append(findings, propertyFinding(ctx, plan.PropertyFramework, "rails", "", []plan.Evidence{{
			Kind:        plan.EvidenceDeclaration,
			Source:      gemfileSource,
			Pointer:     gemPointer(rails.Name),
			Description: "The Gemfile declares Rails.",
		}}))
	}
	return findings
}

func propertyFinding(ctx provider.Context, kind plan.PropertyKind, name, version string, evidence []plan.Evidence) plan.Finding {
	property := plan.Property{
		Kind:       kind,
		Name:       name,
		Version:    version,
		Confidence: plan.ConfidenceHigh,
		Evidence:   evidence,
	}
	return plan.PropertyFinding{
		ProjectPath: ctx.ProjectPath,
		Detector:    providerName,
		Property:    property,
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

func hasRails(manifest gemfile) bool {
	_, ok := manifest.Gems["rails"]
	return ok
}

func stringPtr(value string) *string {
	return &value
}
