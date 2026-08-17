package node

import (
	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

type configuredTool struct {
	name       string
	files      []string
	packageKey func(packageManifest) bool
	framework  bool
}

var configuredTools = []configuredTool{
	{
		name: "eslint",
		files: []string{
			"eslint.config.js", "eslint.config.mjs", "eslint.config.cjs",
			"eslint.config.ts", "eslint.config.mts", "eslint.config.cts",
			".eslintrc", ".eslintrc.js", ".eslintrc.cjs", ".eslintrc.yaml",
			".eslintrc.yml", ".eslintrc.json",
		},
		packageKey: func(m packageManifest) bool { return hasJSONValue(m.ESLintConfig) },
	},
	{
		name: "vitest",
		files: []string{
			"vitest.config.ts", "vitest.config.js", "vitest.config.mts",
			"vitest.config.mjs", "vitest.config.cjs", "vitest.config.cts",
		},
	},
	{
		name: "jest",
		files: []string{
			"jest.config.js", "jest.config.ts", "jest.config.mjs",
			"jest.config.cjs", "jest.config.json", "jest.config.mts",
			"jest.config.cts",
		},
		packageKey: func(m packageManifest) bool { return hasJSONValue(m.Jest) },
	},
	{
		name:  "tsc",
		files: []string{"tsconfig.json"},
	},
	{
		name: "prettier",
		files: []string{
			".prettierrc", ".prettierrc.json", ".prettierrc.yml", ".prettierrc.yaml",
			".prettierrc.js", ".prettierrc.cjs", ".prettierrc.mjs", ".prettierrc.toml",
			"prettier.config.js", "prettier.config.cjs", "prettier.config.mjs",
			"prettier.config.ts",
		},
		packageKey: func(m packageManifest) bool { return hasJSONValue(m.Prettier) },
	},
	{
		name: "vite",
		files: []string{
			"vite.config.ts", "vite.config.js", "vite.config.mts",
			"vite.config.mjs", "vite.config.cjs", "vite.config.cts",
		},
		framework: true,
	},
}

func toolFindings(ctx provider.Context, manifest packageManifest) []plan.Finding {
	var findings []plan.Finding
	for _, tool := range configuredTools {
		evidence := toolEvidence(ctx, manifest, tool)
		if len(evidence) == 0 {
			continue
		}
		findings = append(findings, plan.PropertyFinding{
			ProjectPath: ctx.ProjectPath,
			Detector:    providerName,
			Property: plan.Property{
				Kind:       plan.PropertyFact,
				Name:       "tool.configured",
				Value:      tool.name,
				Confidence: plan.ConfidenceHigh,
				Evidence:   evidence,
			},
		})
		if tool.framework {
			findings = append(findings, plan.PropertyFinding{
				ProjectPath: ctx.ProjectPath,
				Detector:    providerName,
				Property: plan.Property{
					Kind:       plan.PropertyFramework,
					Name:       tool.name,
					Confidence: plan.ConfidenceHigh,
					Evidence:   evidence,
				},
			})
		}
	}
	return findings
}

func toolEvidence(ctx provider.Context, manifest packageManifest, tool configuredTool) []plan.Evidence {
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
	if tool.packageKey != nil && tool.packageKey(manifest) {
		evidence = append(evidence, plan.Evidence{
			Kind:    plan.EvidenceDeclaration,
			Source:  ctx.SourcePath("package.json"),
			Pointer: packageToolPointer(tool.name),
		})
	}
	return evidence
}

func packageToolPointer(tool string) string {
	switch tool {
	case "eslint":
		return "/eslintConfig"
	case "jest":
		return "/jest"
	case "prettier":
		return "/prettier"
	default:
		return "/" + tool
	}
}
