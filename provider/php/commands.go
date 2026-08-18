package php

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/superplanehq/suss/knowledge"
	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

type commandSpec struct {
	name        string
	run         string
	pointer     string
	origin      plan.CommandOrigin
	confidence  plan.Confidence
	evidence    []plan.Evidence
	invocations string
}

func commandFindings(ctx provider.Context, manifest composerManifest) ([]plan.Finding, error) {
	declaredNames := make(map[string]struct{})
	var specs []commandSpec
	for name, raw := range manifest.Scripts {
		if isComposerEventScript(name) {
			continue
		}
		declaredNames[name] = struct{}{}
		specs = append(specs, declaredScriptSpec(ctx, name, raw))
	}

	inferred, err := inferredSpecs(ctx, manifest)
	if err != nil {
		return nil, err
	}
	for _, spec := range inferred {
		if _, declared := declaredNames[spec.name]; !declared {
			specs = append(specs, spec)
		}
	}

	findings := make([]plan.Finding, 0, len(specs))
	for _, spec := range specs {
		command, err := commandFromSpec(ctx, spec)
		if err != nil {
			return nil, err
		}
		findings = append(findings, plan.CommandFinding{ProjectPath: ctx.ProjectPath, Detector: providerName, Command: command})
	}
	return findings, nil
}

func declaredScriptSpec(ctx provider.Context, name string, raw []byte) commandSpec {
	pointer := "/scripts/" + name
	return commandSpec{
		name:        name,
		run:         "composer run-script " + name,
		pointer:     pointer,
		origin:      plan.CommandDeclared,
		confidence:  plan.ConfidenceHigh,
		evidence:    []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: ctx.SourcePath("composer.json"), Pointer: pointer}},
		invocations: scriptInvocations(raw),
	}
}

func inferredSpecs(ctx provider.Context, manifest composerManifest) ([]commandSpec, error) {
	source := ctx.SourcePath("composer.json")
	specs := []commandSpec{conventionSpec(
		source,
		"install dependencies",
		"composer install",
		"/#dependencies",
		plan.ConfidenceMedium,
		"Composer-managed PHP projects conventionally install dependencies with composer install.",
	)}

	testSpec, ok, err := testCommandSpec(ctx, manifest)
	if err != nil {
		return nil, err
	}
	if ok {
		specs = append(specs, testSpec)
	}
	if applicationEvidence := laravelApplicationEvidence(ctx, manifest); len(applicationEvidence) > 0 {
		spec := conventionSpec(
			source,
			"server",
			"php artisan serve",
			"/#server",
			plan.ConfidenceMedium,
			"Laravel applications conventionally start the development server with php artisan serve.",
		)
		spec.evidence = addEvidenceAfterManifest(spec.evidence, applicationEvidence...)
		specs = append(specs, spec)
	}
	return specs, nil
}

func testCommandSpec(ctx provider.Context, manifest composerManifest) (commandSpec, bool, error) {
	source := ctx.SourcePath("composer.json")
	testFile, err := firstPHPTest(ctx.ProjectDir())
	if err != nil {
		return commandSpec{}, false, err
	}
	if testFile == "" && !phpunitConfigured(ctx) && !fileExists(ctx.ProjectDir(), "tests/Pest.php") {
		return commandSpec{}, false, nil
	}

	if applicationEvidence := laravelApplicationEvidence(ctx, manifest); len(applicationEvidence) > 0 {
		spec := conventionSpec(source, "test", "php artisan test", "/#test", plan.ConfidenceHigh, "Laravel applications with tests conventionally run them with php artisan test.")
		evidence := applicationEvidence
		if testFile != "" {
			evidence = append([]plan.Evidence{{Kind: plan.EvidenceFile, Source: ctx.SourcePath(testFile)}}, evidence...)
		}
		spec.evidence = addEvidenceAfterManifest(spec.evidence, evidence...)
		return spec, true, nil
	}

	if hasPest(manifest) {
		spec := conventionSpec(source, "test", "vendor/bin/pest", "/#test", plan.ConfidenceHigh, "PHP projects that declare Pest conventionally run tests with vendor/bin/pest.")
		if testFile != "" {
			spec.evidence = addEvidenceAfterManifest(spec.evidence, plan.Evidence{Kind: plan.EvidenceFile, Source: ctx.SourcePath(testFile)})
		}
		return spec, true, nil
	}

	if testFile == "" && !phpunitConfigured(ctx) {
		return commandSpec{}, false, nil
	}
	spec := conventionSpec(source, "test", "vendor/bin/phpunit", "/#test", plan.ConfidenceHigh, "Composer PHP projects with PHPUnit tests conventionally run them with vendor/bin/phpunit.")
	if testFile != "" {
		spec.evidence = addEvidenceAfterManifest(spec.evidence, plan.Evidence{Kind: plan.EvidenceFile, Source: ctx.SourcePath(testFile)})
	}
	return spec, true, nil
}

func laravelApplicationEvidence(ctx provider.Context, manifest composerManifest) []plan.Evidence {
	if !hasLaravel(manifest) {
		return nil
	}
	var evidence []plan.Evidence
	for _, name := range []string{"artisan", "bootstrap/app.php", "config/app.php"} {
		if fileExists(ctx.ProjectDir(), name) {
			evidence = append(evidence, plan.Evidence{Kind: plan.EvidenceFile, Source: ctx.SourcePath(name)})
		}
	}
	return evidence
}

func phpunitConfigured(ctx provider.Context) bool {
	for _, name := range []string{"phpunit.xml", "phpunit.xml.dist", "phpunit.dist.xml"} {
		if fileExists(ctx.ProjectDir(), name) {
			return true
		}
	}
	return false
}

func conventionSpec(source, name, run, pointer string, confidence plan.Confidence, description string) commandSpec {
	return commandSpec{
		name:       name,
		run:        run,
		pointer:    pointer,
		origin:     plan.CommandInferred,
		confidence: confidence,
		evidence: []plan.Evidence{
			{Kind: plan.EvidenceFile, Source: source},
			{Kind: plan.EvidenceConvention, Source: "php-ecosystem", Pointer: strings.TrimPrefix(pointer, "/#"), Description: description},
		},
		invocations: run,
	}
}

func addEvidenceAfterManifest(evidence []plan.Evidence, additions ...plan.Evidence) []plan.Evidence {
	combined := make([]plan.Evidence, 0, len(evidence)+len(additions))
	combined = append(combined, evidence[0])
	combined = append(combined, additions...)
	return append(combined, evidence[1:]...)
}

func firstPHPTest(root string) (string, error) {
	for _, directory := range []string{"tests", "test"} {
		found, err := firstPHPTestIn(root, directory)
		if err != nil || found != "" {
			return found, err
		}
	}
	return "", nil
}

func firstPHPTestIn(root, directory string) (string, error) {
	testRoot := filepath.Join(root, directory)
	var first string
	err := filepath.WalkDir(testRoot, func(path string, entry fs.DirEntry, err error) error {
		if os.IsNotExist(err) && path == testRoot {
			return fs.SkipAll
		}
		if err != nil {
			return err
		}
		if first != "" {
			return fs.SkipAll
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !matchesPHPTestName(entry.Name()) {
			return nil
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		first = filepath.ToSlash(relative)
		return fs.SkipAll
	})
	if err != nil {
		return "", fmt.Errorf("find PHP tests: %w", err)
	}
	return first, nil
}

func matchesPHPTestName(name string) bool {
	if !strings.HasSuffix(name, ".php") {
		return false
	}
	base := strings.TrimSuffix(name, ".php")
	return strings.HasSuffix(base, "Test") || strings.HasSuffix(base, "_test") || name == "Pest.php"
}

func commandFromSpec(ctx provider.Context, spec commandSpec) (plan.Command, error) {
	source := spec.evidence[0].Source
	id, err := plan.NewCommandID(plan.CommandIdentity{
		ProjectPath: ctx.ProjectPath,
		Provider:    providerName,
		Source:      source,
		Pointer:     spec.pointer,
	})
	if err != nil {
		return plan.Command{}, err
	}

	return plan.Command{
		ID:              id,
		Name:            spec.name,
		Run:             stringPtr(spec.run),
		Directory:       ctx.ProjectPath,
		Scope:           plan.ScopeProject,
		Origin:          spec.origin,
		Confidence:      spec.confidence,
		Evidence:        spec.evidence,
		Interpretations: interpretations(spec),
		Variants:        []plan.CommandVariant{},
	}, nil
}

func interpretations(spec commandSpec) []plan.Interpretation {
	matches := knowledge.InterpretScript(spec.invocations)
	result := make([]plan.Interpretation, 0, len(matches))
	kind := plan.EvidenceConvention
	source := "php-ecosystem"
	pointer := strings.TrimPrefix(spec.pointer, "/#")
	if spec.origin == plan.CommandDeclared {
		kind = plan.EvidenceDeclaration
		source = spec.evidence[0].Source
		pointer = spec.pointer
	}
	for _, match := range matches {
		result = append(result, plan.Interpretation{
			Capability: match.Capability,
			Confidence: match.Confidence,
			Evidence: []plan.Evidence{{
				Kind:        kind,
				Source:      source,
				Pointer:     pointer,
				Description: match.Description,
			}},
		})
	}
	return result
}
