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
	pointer := "/scripts/" + pointerToken(name)
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
	if testFile == "" && !phpunitConfigured(ctx) && !pestConfigured(ctx) {
		return commandSpec{}, false, nil
	}

	if applicationEvidence := laravelApplicationEvidence(ctx, manifest); len(applicationEvidence) > 0 && laravelSupportsArtisanTest(manifest) {
		spec := conventionSpec(source, "test", "php artisan test", "/#test", plan.ConfidenceHigh, "Laravel applications with tests conventionally run them with php artisan test.")
		evidence := applicationEvidence
		if testFile != "" {
			evidence = append([]plan.Evidence{{Kind: plan.EvidenceFile, Source: ctx.SourcePath(testFile)}}, evidence...)
		}
		spec.evidence = addEvidenceAfterManifest(spec.evidence, evidence...)
		return spec, true, nil
	}

	if hasPest(manifest) || pestConfigured(ctx) {
		run := composerBinary(manifest, "pest")
		spec := conventionSpec(source, "test", run, "/#test", plan.ConfidenceHigh, "PHP projects with Pest conventionally run tests with "+run+".")
		var extra []plan.Evidence
		if testFile != "" {
			extra = append(extra, plan.Evidence{Kind: plan.EvidenceFile, Source: ctx.SourcePath(testFile)})
		} else if pestFile := pestConfigFile(ctx); pestFile != "" {
			extra = append(extra, plan.Evidence{Kind: plan.EvidenceFile, Source: ctx.SourcePath(pestFile)})
		}
		extra = append(extra, composerBinEvidence(source, manifest)...)
		if len(extra) > 0 {
			spec.evidence = addEvidenceAfterManifest(spec.evidence, extra...)
		}
		return spec, true, nil
	}

	if runner, pointer, ok := phpTestRunner(manifest); ok {
		run := composerBinary(manifest, runner)
		spec := conventionSpec(source, "test", run, "/#test", plan.ConfidenceHigh, "Composer PHP projects with "+runner+" conventionally run tests with "+run+".")
		var extra []plan.Evidence
		if testFile != "" {
			extra = append(extra, plan.Evidence{Kind: plan.EvidenceFile, Source: ctx.SourcePath(testFile)})
		}
		extra = append(extra, plan.Evidence{Kind: plan.EvidenceDeclaration, Source: source, Pointer: pointer})
		extra = append(extra, composerBinEvidence(source, manifest)...)
		spec.evidence = addEvidenceAfterManifest(spec.evidence, extra...)
		return spec, true, nil
	}

	return commandSpec{}, false, nil
}

func phpTestRunner(manifest composerManifest) (name, pointer string, ok bool) {
	if pointer, ok = packagePointer(manifest, "phpunit/phpunit"); ok {
		return "phpunit", pointer, true
	}
	if pointer, ok = packagePointer(manifest, "symfony/phpunit-bridge"); ok {
		return "simple-phpunit", pointer, true
	}
	return "", "", false
}

func laravelFrameworkConstraint(manifest composerManifest) string {
	if version := strings.TrimSpace(manifest.Require["laravel/framework"]); version != "" {
		return version
	}
	return strings.TrimSpace(manifest.RequireDev["laravel/framework"])
}

// laravelSupportsArtisanTest reports whether the declared laravel/framework
// constraint is known to include only versions that ship `php artisan test`
// (Laravel 7+). Older pins and unevaluable ranges fall through to PHPUnit.
func laravelSupportsArtisanTest(manifest composerManifest) bool {
	constraint := laravelFrameworkConstraint(manifest)
	if constraint == "" {
		return false
	}
	for _, group := range strings.Split(strings.ReplaceAll(constraint, "||", "|"), "|") {
		if !laravelConstraintAtLeastMajor(strings.TrimSpace(group), 7) {
			return false
		}
	}
	return true
}

func laravelConstraintAtLeastMajor(group string, minMajor int) bool {
	group = strings.TrimSpace(group)
	if group == "" || strings.ContainsAny(group, "*xX") {
		return false
	}
	if left, right, ok := strings.Cut(group, " - "); ok {
		left = strings.TrimSpace(left)
		right = strings.TrimSpace(right)
		if left == "" || right == "" {
			return false
		}
		major, ok := constraintMajor(left)
		return ok && major >= minMajor
	}
	proven := false
	for _, token := range laravelConstraintTokens(group) {
		op, major, ok := laravelBound(token)
		if !ok {
			return false
		}
		switch op {
		case "^", "~", ">=", "==", "=", "":
			if major < minMajor {
				return false
			}
			proven = true
		case "<", "<=", "!=", "<>", ">":
			// Upper bounds and exclusions do not prove a lower bound of 7+.
		default:
			return false
		}
	}
	return proven
}

func laravelConstraintTokens(group string) []string {
	fields := strings.Fields(strings.ReplaceAll(group, ",", " "))
	var tokens []string
	for i := 0; i < len(fields); i++ {
		field := fields[i]
		if field == ">=" || field == "<=" || field == ">" || field == "<" || field == "!=" || field == "<>" || field == "==" || field == "=" || field == "^" || field == "~" {
			if i+1 < len(fields) {
				tokens = append(tokens, field+fields[i+1])
				i++
			}
			continue
		}
		tokens = append(tokens, field)
	}
	return tokens
}

func laravelBound(token string) (op string, major int, ok bool) {
	token = strings.TrimSpace(token)
	for _, prefix := range []string{">=", "<=", "!=", "<>", "==", ">", "<", "^", "~", "="} {
		if strings.HasPrefix(token, prefix) {
			major, ok = constraintMajor(token[len(prefix):])
			return prefix, major, ok
		}
	}
	major, ok = constraintMajor(token)
	return "", major, ok
}

func constraintMajor(token string) (int, bool) {
	token = strings.TrimSpace(token)
	token = strings.TrimPrefix(token, "v")
	if token == "" || token[0] < '0' || token[0] > '9' {
		return 0, false
	}
	major := 0
	for _, r := range token {
		if r < '0' || r > '9' {
			break
		}
		major = major*10 + int(r-'0')
	}
	return major, true
}

func laravelApplicationEvidence(ctx provider.Context, manifest composerManifest) []plan.Evidence {
	if !hasLaravel(manifest) || !fileExists(ctx.ProjectDir(), "artisan") {
		return nil
	}
	evidence := []plan.Evidence{{Kind: plan.EvidenceFile, Source: ctx.SourcePath("artisan")}}
	for _, name := range []string{"bootstrap/app.php", "config/app.php"} {
		if fileExists(ctx.ProjectDir(), name) {
			evidence = append(evidence, plan.Evidence{Kind: plan.EvidenceFile, Source: ctx.SourcePath(name)})
		}
	}
	return evidence
}

func pestConfigured(ctx provider.Context) bool {
	return pestConfigFile(ctx) != ""
}

func pestConfigFile(ctx provider.Context) string {
	for _, name := range []string{"tests/Pest.php", "Pest.php"} {
		if fileExists(ctx.ProjectDir(), name) {
			return name
		}
	}
	return ""
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
			if fileExists(path, "composer.json") {
				return fs.SkipDir
			}
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
