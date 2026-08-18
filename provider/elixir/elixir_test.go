package elixir

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

func TestDetectReturnsNothingWithoutMixProject(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{"README.md": "hello\n"}, ".")
	if len(result.Findings) != 0 {
		t.Fatalf("Detect() = %+v, want no findings", result)
	}
}

func TestDetectMixProjectAliasesRuntimesAndTools(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".tool-versions": "erlang 27.2\nelixir 1.18.1-otp-27\nnodejs 22.4.0\n",
		"mix.lock":       "%{}\n",
		"mix.exs": `defmodule Demo.MixProject do
  use Mix.Project

  def project do
    [app: :demo, elixir: "~> 1.18", aliases: aliases(), deps: deps()]
  end

  defp deps do
    [
      {:phoenix, "~> 1.7"},
      {:credo, "~> 1.7", only: [:dev, :test]},
      {:dialyxir, "~> 1.4", only: [:dev, :test]}
    ]
  end

  defp aliases do
    [
      setup: ["deps.get", "ecto.setup"],
      test: ["compile --warnings-as-errors", "test"],
      "assets.deploy": ["tailwind default --minify", "phx.digest"]
    ]
  end
end
`,
		".credo.exs":               "%{}\n",
		"dialyzer.ignore-warnings": "ignore\n",
		"test/demo_test.exs":       "defmodule DemoTest do\n  use ExUnit.Case\nend\n",
		"lib/mix/tasks/demo.seed.ex": `defmodule Mix.Tasks.Demo.Seed do
  use Mix.Task
end
`,
		"lib/mix/tasks/download_country_database.ex": `defmodule Mix.Tasks.DownloadCountryDatabase do
  use Mix.Task
end
`,
	}, ".")

	if !hasLanguage(result, "elixir") {
		t.Fatalf("missing Elixir language in %+v", result.Findings)
	}
	if !hasFramework(result, "phoenix") {
		t.Fatalf("missing Phoenix framework in %+v", result.Findings)
	}
	if !hasPackageManager(result, "mix") {
		t.Fatalf("missing Mix package manager in %+v", result.Findings)
	}
	if !hasRuntimeRequirement(result, "elixir", "1.18.1-otp-27") ||
		!hasRuntimeRequirement(result, "erlang", "27.2") {
		t.Fatalf("missing .tool-versions runtimes in %+v", result.Findings)
	}
	if !hasRuntimeRequirement(result, "elixir", "~> 1.18") {
		t.Fatalf("missing mix.exs Elixir constraint in %+v", result.Findings)
	}

	commands := commandsByName(result)
	assertCommand(t, commands["setup"], "mix setup", plan.CommandDeclared, plan.CapabilityDependenciesInstall)
	assertCommand(t, commands["test"], "mix test", plan.CommandDeclared, plan.CapabilityTestRun)
	assertCommand(t, commands["demo.seed"], "mix demo.seed", plan.CommandDeclared, "")
	assertCommand(t, commands["download_country_database"], "mix download_country_database", plan.CommandDeclared, "")
	assertCommand(t, commands["compile"], "mix compile", plan.CommandInferred, plan.CapabilityArtifactBuild)
	if _, ok := commands["dependencies"]; ok {
		t.Fatal("unexpected generic dependency command name")
	}
	if got := factValues(result, "tool.configured"); !slices.Contains(got, "credo") || !slices.Contains(got, "dialyzer") {
		t.Fatalf("configured tools = %v, want credo and dialyzer", got)
	}
}

func TestDetectReadsToolVersionsFromRepositoryAncestor(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".tool-versions": "erlang 26.2\nelixir 1.16.2-otp-26\n",
		"apps/web/mix.exs": `defmodule Web.MixProject do
  use Mix.Project
  def project, do: [app: :web, elixir: "~> 1.16"]
end
`,
	}, "apps/web")

	if !hasRuntimeRequirement(result, "elixir", "1.16.2-otp-26") {
		t.Fatalf("missing inherited Elixir pin in %+v", result.Findings)
	}
	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if ok && item.Requirement.Name == "elixir" && item.Requirement.Version == "1.16.2-otp-26" && item.Requirement.Evidence[0].Source != ".tool-versions" {
			t.Fatalf("pin evidence source = %q, want .tool-versions", item.Requirement.Evidence[0].Source)
		}
	}
}

func detectFiles(t *testing.T, files map[string]string, projectPath string) provider.Result {
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

	result, err := Provider{}.Detect(provider.Context{RepositoryRoot: root, ProjectPath: projectPath})
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	return result
}

func commandsByName(result provider.Result) map[string]plan.Command {
	out := make(map[string]plan.Command)
	for _, finding := range result.Findings {
		item, ok := finding.(plan.CommandFinding)
		if ok {
			out[item.Command.Name] = item.Command
		}
	}
	return out
}

func assertCommand(t *testing.T, command plan.Command, run string, origin plan.CommandOrigin, capability plan.Capability) {
	t.Helper()
	if command.Run == nil || *command.Run != run || command.Origin != origin {
		t.Fatalf("command = %+v, want run %q and origin %s", command, run, origin)
	}
	if capability != "" && !hasCapability(command, capability) {
		t.Fatalf("command interpretations = %+v, want %s", command.Interpretations, capability)
	}
}

func hasCapability(command plan.Command, capability plan.Capability) bool {
	for _, interpretation := range command.Interpretations {
		if interpretation.Capability == capability {
			return true
		}
	}
	return false
}

func hasLanguage(result provider.Result, name string) bool {
	return hasProperty(result, plan.PropertyLanguage, name)
}

func hasFramework(result provider.Result, name string) bool {
	return hasProperty(result, plan.PropertyFramework, name)
}

func hasPackageManager(result provider.Result, name string) bool {
	return hasProperty(result, plan.PropertyPackageManager, name)
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

func hasRuntimeRequirement(result provider.Result, name, version string) bool {
	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if ok && item.Requirement.Kind == plan.RequirementRuntime && item.Requirement.Name == name && item.Requirement.Version == version {
			return true
		}
	}
	return false
}

func factValues(result provider.Result, name string) []string {
	var values []string
	for _, finding := range result.Findings {
		item, ok := finding.(plan.PropertyFinding)
		if ok && item.Property.Name == name {
			values = append(values, item.Property.Value)
		}
	}
	return values
}
