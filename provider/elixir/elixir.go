// Package elixir detects Elixir projects from mix.exs and related files.
package elixir

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

const providerName = "elixir"

// Provider detects one Mix project.
type Provider struct{}

// Name returns the stable provider identifier.
func (Provider) Name() string {
	return providerName
}

// Detect inspects one project root. It returns no findings without mix.exs.
func (Provider) Detect(ctx provider.Context) (provider.Result, error) {
	contents, ok, err := readMixProject(ctx)
	if err != nil || !ok {
		return provider.Result{}, err
	}

	parsed := parseMixProject(contents)
	result := provider.Result{Findings: projectFindings(ctx, parsed)}
	runtimes, err := runtimeFindings(ctx, parsed.ElixirVersion)
	if err != nil {
		return provider.Result{}, err
	}
	result.Findings = append(result.Findings, runtimes...)
	result.Findings = append(result.Findings, configuredToolFindings(ctx, parsed)...)

	commands, err := commandFindings(ctx, parsed)
	if err != nil {
		return provider.Result{}, err
	}
	result.Findings = append(result.Findings, commands...)
	return result, nil
}

func readMixProject(ctx provider.Context) (string, bool, error) {
	contents, err := os.ReadFile(filepath.Join(ctx.ProjectDir(), "mix.exs"))
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read mix.exs: %w", err)
	}
	return string(contents), true, nil
}

func projectFindings(ctx provider.Context, parsed mixProject) []plan.Finding {
	source := ctx.SourcePath("mix.exs")
	declaration := []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: source}}
	findings := []plan.Finding{
		propertyFinding(ctx, plan.PropertyLanguage, "elixir", "", declaration),
		propertyFinding(ctx, plan.PropertyPackageManager, "mix", "", declaration),
	}
	if parsed.HasPhoenix {
		findings = append(findings, propertyFinding(ctx, plan.PropertyFramework, "phoenix", "", []plan.Evidence{{
			Kind:        plan.EvidenceDeclaration,
			Source:      source,
			Pointer:     "/deps/phoenix",
			Description: "The Mix dependency list includes Phoenix.",
		}}))
	}
	return findings
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

func configuredToolFindings(ctx provider.Context, parsed mixProject) []plan.Finding {
	checks := []struct {
		name  string
		files []string
		inMix bool
	}{
		{name: "credo", files: []string{".credo.exs"}},
		{name: "dialyzer", files: []string{"dialyzer.ignore-warnings", ".dialyzer_ignore.exs", ".dialyzer_ignore_warnings"}, inMix: parsed.HasDialyzerConfig},
	}

	var findings []plan.Finding
	for _, check := range checks {
		var evidence []plan.Evidence
		for _, name := range check.files {
			if fileExists(filepath.Join(ctx.ProjectDir(), name)) {
				evidence = append(evidence, plan.Evidence{Kind: plan.EvidenceConfiguration, Source: ctx.SourcePath(name)})
			}
		}
		if check.inMix {
			evidence = append(evidence, plan.Evidence{Kind: plan.EvidenceConfiguration, Source: ctx.SourcePath("mix.exs"), Pointer: "/project/dialyzer"})
		}
		if len(evidence) > 0 {
			findings = append(findings, propertyFinding(ctx, plan.PropertyFact, "tool.configured", check.name, evidence))
		}
	}
	return findings
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func stringPtr(value string) *string {
	return &value
}

func pointerToken(value string) string {
	value = strings.ReplaceAll(value, "~", "~0")
	return strings.ReplaceAll(value, "/", "~1")
}
