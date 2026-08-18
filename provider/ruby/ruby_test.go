package ruby

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

func TestDetectReturnsNothingWithoutGemfile(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{"README.md": "hello\n"})
	if len(result.Findings) != 0 {
		t.Fatalf("Detect() = %+v, want no findings", result)
	}
}

func TestDetectRailsProjectRuntimesBundlerToolsAndRSpec(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"Gemfile": `source "https://rubygems.org"
ruby "~> 3.3"
gem "rails", "~> 8.0"
gem "rspec-rails", group: [:development, :test]
gem "rubocop", require: false
gem "sorbet-static", require: false
`,
		"Gemfile.lock": `GEM
  specs:
    rails (8.0.2)

RUBY VERSION
   ruby 3.3.6p108

BUNDLED WITH
   2.5.11
`,
		".ruby-version":            "3.3.6\n",
		".tool-versions":           "ruby 3.3.6\nnodejs 22.4.0\n",
		"config/application.rb":    "class Application < Rails::Application\nend\n",
		"bin/rails":                "#!/usr/bin/env ruby\n",
		"bin/rspec":                "#!/usr/bin/env ruby\n",
		"spec/models/user_spec.rb": "RSpec.describe User do\nend\n",
		".rspec":                   "--format documentation\n",
		".rubocop.yml":             "AllCops:\n  NewCops: enable\n",
		"sorbet/config":            "--dir .\n",
	})

	if !hasProperty(result, plan.PropertyLanguage, "ruby") {
		t.Fatalf("missing Ruby language in %+v", result.Findings)
	}
	if !hasProperty(result, plan.PropertyFramework, "rails") {
		t.Fatalf("missing Rails framework in %+v", result.Findings)
	}
	if !hasPackageManager(result, "bundler", "2.5.11") {
		t.Fatalf("missing Bundler 2.5.11 in %+v", result.Findings)
	}
	for _, version := range []string{"3.3.6", "~> 3.3"} {
		if !hasRuntime(result, version) {
			t.Fatalf("missing Ruby runtime %q in %+v", version, result.Findings)
		}
	}

	commands := commandsByName(result)
	assertCommand(t, commands["install dependencies"], "bundle install", plan.CapabilityDependenciesInstall)
	assertCommand(t, commands["test"], "bin/rspec", plan.CapabilityTestRun)
	assertCommand(t, commands["server"], "bin/rails server", plan.CapabilityApplicationRun)

	tools := factValues(result, "tool.configured")
	for _, tool := range []string{"rspec", "rubocop", "sorbet"} {
		if !slices.Contains(tools, tool) {
			t.Fatalf("configured tools = %v, want %s", tools, tool)
		}
	}
}

func TestDetectRailsMinitestWithoutBinWrappers(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"Gemfile":                  "source \"https://rubygems.org\"\ngem \"rails\", \"~> 7.2\"\n",
		"config/application.rb":    "class Application < Rails::Application\nend\n",
		"test/models/user_test.rb": "class UserTest < ActiveSupport::TestCase\nend\n",
	})

	commands := commandsByName(result)
	assertCommand(t, commands["test"], "bundle exec rails test", plan.CapabilityTestRun)
	assertCommand(t, commands["server"], "bundle exec rails server", plan.CapabilityApplicationRun)
}

func TestDetectRailsDependencyWithoutApplicationDoesNotInferServer(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"Gemfile":                 "source \"https://rubygems.org\"\ngem \"rails\", \"~> 7.2\"\n",
		"Rakefile":                "# Rake::TestTask.new(:test)\n",
		"test/models/gem_test.rb": "class GemTest < Minitest::Test\nend\n",
	})

	commands := commandsByName(result)
	if _, ok := commands["server"]; ok {
		t.Fatal("Rails dependency without application evidence unexpectedly has a server command")
	}
	if _, ok := commands["test"]; ok {
		t.Fatal("commented Rake test task unexpectedly produced a test command")
	}
}

func TestDetectRubyRakeTests(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"Gemfile":               "source \"https://rubygems.org\"\ngemspec\n",
		"Rakefile":              "require \"rake/testtask\"\nRake::TestTask.new(:test)\n",
		"test/test_widget.rb":   "class WidgetTest < Minitest::Test\nend\n",
		"lib/widget/version.rb": "module Widget\n  VERSION = \"1.0.0\"\nend\n",
	})

	commands := commandsByName(result)
	assertCommand(t, commands["test"], "bundle exec rake test", plan.CapabilityTestRun)
	if _, ok := commands["server"]; ok {
		t.Fatal("non-Rails Ruby project unexpectedly has a server command")
	}
}

func TestDetectBundlerGemBuildTask(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"Gemfile":        "source \"https://rubygems.org\"\ngemspec\n",
		"Rakefile":       "require \"bundler/gem_tasks\"\n",
		"widget.gemspec": "Gem::Specification.new do |spec|\nend\n",
	})

	assertCommand(t, commandsByName(result)["build"], "bundle exec rake build", plan.CapabilityArtifactBuild)
}

func TestDetectReportsConflictingRuntimePins(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"Gemfile":        "source \"https://rubygems.org\"\n",
		".ruby-version":  "3.3.6\n",
		".tool-versions": "ruby 3.4.1\n",
	})

	if len(result.Conflicts) != 1 || result.Conflicts[0].Subject != "runtime.ruby.version" {
		t.Fatalf("conflicts = %+v, want one Ruby runtime conflict", result.Conflicts)
	}
	if !hasRuntime(result, "3.3.6") || !hasRuntime(result, "3.4.1") {
		t.Fatalf("runtime findings = %+v, want both conflicting pins", result.Findings)
	}
}

func TestDetectMergesMatchingRuntimeEvidence(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"Gemfile":       "source \"https://rubygems.org\"\nruby \"3.3.6\"\n",
		".ruby-version": "3.3.6\n",
	})

	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if !ok || item.Requirement.Name != "ruby" || item.Requirement.Version != "3.3.6" {
			continue
		}
		sources := make([]string, 0, len(item.Requirement.Evidence))
		for _, evidence := range item.Requirement.Evidence {
			sources = append(sources, evidence.Source)
		}
		if !slices.Contains(sources, ".ruby-version") || !slices.Contains(sources, "Gemfile") {
			t.Fatalf("runtime evidence = %v, want .ruby-version and Gemfile", sources)
		}
		return
	}
	t.Fatal("missing merged Ruby 3.3.6 requirement")
}

func detectFiles(t *testing.T, files map[string]string) provider.Result {
	t.Helper()

	root := t.TempDir()
	for name, contents := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("os.MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatalf("os.WriteFile() error = %v", err)
		}
	}

	result, err := Provider{}.Detect(provider.Context{RepositoryRoot: root, ProjectPath: "."})
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	return result
}

func hasProperty(result provider.Result, kind plan.PropertyKind, name string) bool {
	for _, finding := range result.Findings {
		item, ok := finding.(plan.PropertyFinding)
		if ok && item.Property.Kind == kind && item.Property.Name == name {
			return true
		}
	}
	return false
}

func hasPackageManager(result provider.Result, name, version string) bool {
	for _, finding := range result.Findings {
		item, ok := finding.(plan.PropertyFinding)
		if ok && item.Property.Kind == plan.PropertyPackageManager && item.Property.Name == name && item.Property.Version == version {
			return true
		}
	}
	return false
}

func hasRuntime(result provider.Result, version string) bool {
	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if ok && item.Requirement.Kind == plan.RequirementRuntime && item.Requirement.Name == "ruby" && item.Requirement.Version == version {
			return true
		}
	}
	return false
}

func commandsByName(result provider.Result) map[string]plan.Command {
	commands := make(map[string]plan.Command)
	for _, finding := range result.Findings {
		item, ok := finding.(plan.CommandFinding)
		if ok {
			commands[item.Command.Name] = item.Command
		}
	}
	return commands
}

func assertCommand(t *testing.T, command plan.Command, run string, capability plan.Capability) {
	t.Helper()
	if command.Run == nil || *command.Run != run || command.Origin != plan.CommandInferred {
		t.Fatalf("command = %+v, want inferred %q", command, run)
	}
	for _, interpretation := range command.Interpretations {
		if interpretation.Capability == capability {
			return
		}
	}
	t.Fatalf("command interpretations = %+v, want %s", command.Interpretations, capability)
}

func factValues(result provider.Result, name string) []string {
	var values []string
	for _, finding := range result.Findings {
		item, ok := finding.(plan.PropertyFinding)
		if ok && item.Property.Kind == plan.PropertyFact && item.Property.Name == name {
			values = append(values, item.Property.Value)
		}
	}
	return values
}
