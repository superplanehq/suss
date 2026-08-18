package rust

import (
	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

type configuredTool struct {
	name  string
	files []string
}

var configuredTools = []configuredTool{
	{name: "clippy", files: []string{"clippy.toml", ".clippy.toml"}},
	{name: "rustfmt", files: []string{"rustfmt.toml", ".rustfmt.toml"}},
	{name: "nextest", files: []string{".config/nextest.toml", "nextest.toml"}},
	{name: "cargo-deny", files: []string{"deny.toml"}},
}

func toolFindings(ctx provider.Context) []plan.Finding {
	var findings []plan.Finding
	for _, tool := range configuredTools {
		var evidence []plan.Evidence
		for _, name := range tool.files {
			if !fileExists(ctx.ProjectDir(), name) {
				continue
			}
			evidence = append(evidence, plan.Evidence{
				Kind:   plan.EvidenceConfiguration,
				Source: ctx.SourcePath(name),
			})
		}
		if len(evidence) == 0 {
			continue
		}
		findings = append(findings, propertyFinding(ctx, plan.PropertyFact, "tool.configured", tool.name, evidence))
	}
	return findings
}
