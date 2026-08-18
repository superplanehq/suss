package php

import (
	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

type configuredTool struct {
	name         string
	files        []string
	dependencies []string
}

var configuredTools = []configuredTool{
	{name: "phpunit", files: []string{"phpunit.xml", "phpunit.xml.dist", "phpunit.dist.xml"}, dependencies: []string{"phpunit/phpunit"}},
	{name: "pest", files: []string{"tests/Pest.php", "Pest.php"}, dependencies: []string{"pestphp/pest"}},
	{name: "phpstan", files: []string{"phpstan.neon", "phpstan.neon.dist", "phpstan.dist.neon"}, dependencies: []string{"phpstan/phpstan", "larastan/larastan"}},
	{name: "psalm", files: []string{"psalm.xml", "psalm.xml.dist"}, dependencies: []string{"vimeo/psalm"}},
	{name: "php-cs-fixer", files: []string{".php-cs-fixer.php", ".php-cs-fixer.dist.php"}, dependencies: []string{"friendsofphp/php-cs-fixer"}},
	{name: "phpcs", files: []string{"phpcs.xml", "phpcs.xml.dist", ".phpcs.xml", ".phpcs.xml.dist"}, dependencies: []string{"squizlabs/php_codesniffer"}},
	{name: "pint", files: []string{"pint.json"}, dependencies: []string{"laravel/pint"}},
}

func configuredToolFindings(ctx provider.Context, manifest composerManifest) []plan.Finding {
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

func configuredToolEvidence(ctx provider.Context, manifest composerManifest, tool configuredTool) []plan.Evidence {
	var evidence []plan.Evidence
	for _, name := range tool.files {
		if fileExists(ctx.ProjectDir(), name) {
			evidence = append(evidence, plan.Evidence{Kind: plan.EvidenceConfiguration, Source: ctx.SourcePath(name)})
		}
	}
	for _, name := range tool.dependencies {
		pointer, ok := packagePointer(manifest, name)
		if !ok {
			continue
		}
		evidence = append(evidence, plan.Evidence{
			Kind:    plan.EvidenceDeclaration,
			Source:  ctx.SourcePath("composer.json"),
			Pointer: pointer,
		})
	}
	return evidence
}

func hasLaravel(manifest composerManifest) bool {
	return hasPackage(manifest, "laravel/framework")
}

func hasPest(manifest composerManifest) bool {
	return hasPackage(manifest, "pestphp/pest")
}
