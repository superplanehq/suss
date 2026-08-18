package java

import (
	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

type configuredTool struct {
	name          string
	files         []string
	mavenPlugins  []string
	gradlePlugins []string
}

var configuredTools = []configuredTool{
	{
		name:          "checkstyle",
		files:         []string{"checkstyle.xml", "config/checkstyle/checkstyle.xml", "src/checkstyle/checkstyle.xml", ".checkstyle"},
		mavenPlugins:  []string{"maven-checkstyle-plugin"},
		gradlePlugins: []string{"checkstyle"},
	},
	{
		name:          "pmd",
		files:         []string{"pmd.xml", "config/pmd/pmd.xml"},
		mavenPlugins:  []string{"maven-pmd-plugin"},
		gradlePlugins: []string{"pmd"},
	},
	{
		name:          "spotbugs",
		files:         []string{"spotbugs.xml", "config/spotbugs/spotbugs.xml"},
		mavenPlugins:  []string{"spotbugs-maven-plugin"},
		gradlePlugins: []string{"com.github.spotbugs", "com.github.spotbugs.spotbugs"},
	},
	{
		name:          "spotless",
		gradlePlugins: []string{"com.diffplug.spotless"},
	},
	{
		name:         "errorprone",
		mavenPlugins: []string{"error_prone_core"},
		gradlePlugins: []string{
			"net.ltgt.errorprone",
		},
	},
}

func configuredToolFindings(ctx provider.Context, project javaProject) []plan.Finding {
	var findings []plan.Finding
	for _, tool := range configuredTools {
		evidence := configuredToolEvidence(ctx, project, tool)
		if len(evidence) == 0 {
			continue
		}
		findings = append(findings, factFinding(ctx, "tool.configured", tool.name, evidence))
	}
	return findings
}

func configuredToolEvidence(ctx provider.Context, project javaProject, tool configuredTool) []plan.Evidence {
	var evidence []plan.Evidence
	for _, name := range tool.files {
		if fileExists(ctx.ProjectDir(), name) {
			evidence = append(evidence, plan.Evidence{Kind: plan.EvidenceConfiguration, Source: ctx.SourcePath(name)})
		}
	}
	if project.Maven != nil {
		for _, plugin := range tool.mavenPlugins {
			if project.Maven.hasPlugin(plugin) {
				evidence = append(evidence, plan.Evidence{
					Kind:    plan.EvidenceDeclaration,
					Source:  ctx.SourcePath(project.Maven.Source),
					Pointer: "/build/plugins/" + pointerToken(plugin),
				})
			}
		}
	}
	if project.Gradle != nil {
		for _, plugin := range tool.gradlePlugins {
			if project.Gradle.hasPlugin(plugin) {
				evidence = append(evidence, plan.Evidence{
					Kind:    plan.EvidenceDeclaration,
					Source:  ctx.SourcePath(project.Gradle.Source),
					Pointer: "/plugins/" + pointerToken(plugin),
				})
			}
		}
	}
	return evidence
}
