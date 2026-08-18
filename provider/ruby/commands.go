package ruby

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/superplanehq/suss/knowledge"
	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

type commandSpec struct {
	name        string
	run         string
	pointer     string
	confidence  plan.Confidence
	evidence    []plan.Evidence
	invocations string
}

func commandFindings(ctx provider.Context, manifest gemfile) ([]plan.Finding, error) {
	source := ctx.SourcePath("Gemfile")
	specs := []commandSpec{conventionSpec(
		source,
		"install dependencies",
		"bundle install",
		"/#dependencies",
		plan.ConfidenceMedium,
		"Bundler-managed Ruby projects conventionally install dependencies with bundle install.",
	)}

	testSpec, ok, err := testCommandSpec(ctx, manifest)
	if err != nil {
		return nil, err
	}
	if ok {
		specs = append(specs, testSpec)
	}
	hasBuildTask, err := hasBundlerGemTasks(ctx)
	if err != nil {
		return nil, err
	}
	if hasBuildTask {
		spec := conventionSpec(
			source,
			"build",
			"bundle exec rake build",
			"/#build",
			plan.ConfidenceHigh,
			"A Rakefile loading Bundler gem tasks defines the conventional rake build task.",
		)
		spec.evidence = addEvidenceAfterManifest(spec.evidence, plan.Evidence{Kind: plan.EvidenceDeclaration, Source: ctx.SourcePath("Rakefile"), Pointer: "/bundler/gem_tasks"})
		specs = append(specs, spec)
	}
	if applicationEvidence := railsApplicationEvidence(ctx, manifest); len(applicationEvidence) > 0 {
		run := "bundle exec rails server"
		if fileExists(ctx.ProjectDir(), "bin/rails") {
			run = "bin/rails server"
		}
		spec := conventionSpec(
			source,
			"server",
			run,
			"/#server",
			plan.ConfidenceMedium,
			"Rails applications conventionally start the development server with rails server.",
		)
		spec.evidence = addEvidenceAfterManifest(spec.evidence, applicationEvidence...)
		specs = append(specs, spec)
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

func railsApplicationEvidence(ctx provider.Context, manifest gemfile) []plan.Evidence {
	if !hasRails(manifest) {
		return nil
	}
	var evidence []plan.Evidence
	for _, name := range []string{"bin/rails", "config/application.rb"} {
		if fileExists(ctx.ProjectDir(), name) {
			evidence = append(evidence, plan.Evidence{Kind: plan.EvidenceFile, Source: ctx.SourcePath(name)})
		}
	}
	return evidence
}

func testCommandSpec(ctx provider.Context, manifest gemfile) (commandSpec, bool, error) {
	source := ctx.SourcePath("Gemfile")
	if testFile, err := firstRubyTest(ctx.ProjectDir(), "spec", "_spec.rb"); err != nil {
		return commandSpec{}, false, err
	} else if testFile != "" {
		run := "bundle exec rspec"
		if fileExists(ctx.ProjectDir(), "bin/rspec") {
			run = "bin/rspec"
		}
		spec := conventionSpec(source, "test", run, "/#test", plan.ConfidenceHigh, "Ruby projects with RSpec examples conventionally run them with rspec.")
		spec.evidence = addEvidenceAfterManifest(spec.evidence, plan.Evidence{Kind: plan.EvidenceFile, Source: ctx.SourcePath(testFile)})
		return spec, true, nil
	}

	testFile, err := firstRubyTest(ctx.ProjectDir(), "test", "_test.rb")
	if err != nil || testFile == "" {
		return commandSpec{}, false, err
	}
	if applicationEvidence := railsApplicationEvidence(ctx, manifest); len(applicationEvidence) > 0 {
		run := "bundle exec rails test"
		if fileExists(ctx.ProjectDir(), "bin/rails") {
			run = "bin/rails test"
		}
		spec := conventionSpec(source, "test", run, "/#test", plan.ConfidenceHigh, "Rails applications with Minitest files conventionally run them with rails test.")
		spec.evidence = addEvidenceAfterManifest(spec.evidence, append(
			[]plan.Evidence{{Kind: plan.EvidenceFile, Source: ctx.SourcePath(testFile)}},
			applicationEvidence...,
		)...)
		return spec, true, nil
	}

	hasTask, err := hasRakeTestTask(ctx)
	if err != nil || !hasTask {
		return commandSpec{}, false, err
	}
	spec := conventionSpec(source, "test", "bundle exec rake test", "/#test", plan.ConfidenceHigh, "Ruby projects declaring Rake::TestTask conventionally run the test task with rake test.")
	spec.evidence = addEvidenceAfterManifest(spec.evidence,
		plan.Evidence{Kind: plan.EvidenceFile, Source: ctx.SourcePath("Rakefile")},
		plan.Evidence{Kind: plan.EvidenceFile, Source: ctx.SourcePath(testFile)},
	)
	return spec, true, nil
}

func conventionSpec(source, name, run, pointer string, confidence plan.Confidence, description string) commandSpec {
	return commandSpec{
		name:       name,
		run:        run,
		pointer:    pointer,
		confidence: confidence,
		evidence: []plan.Evidence{
			{Kind: plan.EvidenceFile, Source: source},
			{Kind: plan.EvidenceConvention, Source: "ruby-ecosystem", Pointer: strings.TrimPrefix(pointer, "/#"), Description: description},
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

func firstRubyTest(root, directory, suffix string) (string, error) {
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
		if entry.Type()&os.ModeSymlink != 0 || !matchesRubyTestName(entry.Name(), suffix) {
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
		return "", fmt.Errorf("find Ruby tests: %w", err)
	}
	return first, nil
}

func matchesRubyTestName(name, suffix string) bool {
	if strings.HasSuffix(name, suffix) {
		return true
	}
	return suffix == "_test.rb" && strings.HasPrefix(name, "test_") && strings.HasSuffix(name, ".rb")
}

var rakeTestTaskPattern = regexp.MustCompile(`(?m)\bRake::TestTask\.new\s*(?:\(\s*:test\s*\)|:test\b|do\b)`)

func hasRakeTestTask(ctx provider.Context) (bool, error) {
	contents, ok, err := readRakefile(ctx)
	if err != nil || !ok {
		return false, err
	}
	return rakeTestTaskPattern.MatchString(stripRubyComments(contents)), nil
}

var bundlerGemTasksPattern = regexp.MustCompile(`(?m)(?:require\s*[\( ]?\s*["']bundler/gem_tasks["']|Bundler::GemHelper\.install_tasks\b)`)

func hasBundlerGemTasks(ctx provider.Context) (bool, error) {
	contents, ok, err := readRakefile(ctx)
	if err != nil || !ok {
		return false, err
	}
	return bundlerGemTasksPattern.MatchString(stripRubyComments(contents)), nil
}

func readRakefile(ctx provider.Context) (string, bool, error) {
	contents, err := os.ReadFile(filepath.Join(ctx.ProjectDir(), "Rakefile"))
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read Rakefile: %w", err)
	}
	return string(contents), true, nil
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
		Origin:          plan.CommandInferred,
		Confidence:      spec.confidence,
		Evidence:        spec.evidence,
		Interpretations: interpretations(spec),
		Variants:        []plan.CommandVariant{},
	}, nil
}

func interpretations(spec commandSpec) []plan.Interpretation {
	matches := knowledge.InterpretScript(spec.invocations)
	result := make([]plan.Interpretation, 0, len(matches))
	for _, match := range matches {
		result = append(result, plan.Interpretation{
			Capability: match.Capability,
			Confidence: match.Confidence,
			Evidence: []plan.Evidence{{
				Kind:        plan.EvidenceConvention,
				Source:      "ruby-ecosystem",
				Pointer:     strings.TrimPrefix(spec.pointer, "/#"),
				Description: match.Description,
			}},
		})
	}
	return result
}
