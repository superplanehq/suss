package ruby

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

type configuredTool struct {
	name         string
	files        []string
	dependencies []string
}

var configuredTools = []configuredTool{
	{name: "rspec", files: []string{".rspec", "spec/spec_helper.rb", "spec/rails_helper.rb"}, dependencies: []string{"rspec", "rspec-rails"}},
	{name: "rubocop", files: []string{".rubocop.yml", ".rubocop.yaml", ".rubocop_todo.yml"}, dependencies: []string{"rubocop"}},
	{name: "standard", files: []string{".standard.yml", ".standard.yaml"}, dependencies: []string{"standard"}},
	{name: "sorbet", files: []string{"sorbet/config"}, dependencies: []string{"sorbet", "sorbet-static"}},
	{name: "brakeman", files: []string{"brakeman.yml", "config/brakeman.ignore"}, dependencies: []string{"brakeman"}},
}

func configuredToolFindings(ctx provider.Context, manifest gemfile) []plan.Finding {
	var findings []plan.Finding
	for _, tool := range configuredTools {
		evidence := configuredToolEvidence(ctx, manifest, tool)
		if len(evidence) == 0 {
			continue
		}
		findings = append(findings, factFinding(ctx, "tool.configured", tool.name, evidence))
	}
	return findings
}

func configuredToolEvidence(ctx provider.Context, manifest gemfile, tool configuredTool) []plan.Evidence {
	var evidence []plan.Evidence
	for _, name := range tool.files {
		if fileExists(ctx.ProjectDir(), name) {
			evidence = append(evidence, plan.Evidence{Kind: plan.EvidenceConfiguration, Source: ctx.SourcePath(name)})
		}
	}
	for name := range manifest.Gems {
		if !matchesToolDependency(name, tool.dependencies) {
			continue
		}
		evidence = append(evidence, plan.Evidence{
			Kind:    plan.EvidenceDeclaration,
			Source:  ctx.SourcePath("Gemfile"),
			Pointer: gemPointer(name),
		})
	}
	return evidence
}

func matchesToolDependency(name string, dependencies []string) bool {
	for _, dependency := range dependencies {
		if name == dependency || strings.HasPrefix(name, dependency+"-") {
			return true
		}
	}
	return false
}

func fileExists(dir, name string) bool {
	info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name)))
	return err == nil && !info.IsDir()
}
