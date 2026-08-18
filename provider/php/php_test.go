package php

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

func TestDetectReturnsNothingWithoutComposerJSON(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{"README.md": "hello\n"})
	if len(result.Findings) != 0 {
		t.Fatalf("Detect() = %+v, want no findings", result)
	}
}

func TestDetectLaravelProjectRuntimesComposerToolsAndArtisan(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"composer.json": `{
  "require": {
    "php": "^8.3",
    "laravel/framework": "^11.0"
  },
  "require-dev": {
    "phpunit/phpunit": "^11.0",
    "laravel/pint": "^1.21",
    "phpstan/phpstan": "^2.0"
  },
  "scripts": {
    "test": "@php artisan test",
    "post-autoload-dump": "@php artisan package:discover"
  }
}`,
		"composer.lock":                 "{}\n",
		".php-version":                  "8.3.6\n",
		".tool-versions":                "php 8.3.6\nnodejs 22.4.0\n",
		"artisan":                       "#!/usr/bin/env php\n",
		"bootstrap/app.php":             "<?php\n",
		"phpunit.xml":                   "<phpunit></phpunit>\n",
		"pint.json":                     "{}\n",
		"phpstan.neon":                  "parameters:\n  level: 6\n",
		"tests/Feature/ExampleTest.php": "<?php\nclass ExampleTest {}\n",
	})

	if !hasProperty(result, plan.PropertyLanguage, "php") {
		t.Fatalf("missing PHP language in %+v", result.Findings)
	}
	if !hasProperty(result, plan.PropertyFramework, "laravel") {
		t.Fatalf("missing Laravel framework in %+v", result.Findings)
	}
	if !hasPackageManager(result, "composer") {
		t.Fatalf("missing Composer in %+v", result.Findings)
	}
	for _, version := range []string{"8.3.6", "^8.3"} {
		if !hasRuntime(result, version) {
			t.Fatalf("missing PHP runtime %q in %+v", version, result.Findings)
		}
	}

	commands := commandsByName(result)
	assertCommand(t, commands["install dependencies"], "composer install", plan.CommandInferred, plan.CapabilityDependenciesInstall)
	assertCommand(t, commands["test"], "composer run-script test", plan.CommandDeclared, plan.CapabilityTestRun)
	assertCommand(t, commands["server"], "php artisan serve", plan.CommandInferred, plan.CapabilityApplicationRun)
	if _, ok := commands["post-autoload-dump"]; ok {
		t.Fatal("Composer event hook was emitted as a command")
	}

	tools := factValues(result, "tool.configured")
	for _, tool := range []string{"phpunit", "phpstan", "pint"} {
		if !slices.Contains(tools, tool) {
			t.Fatalf("configured tools = %v, want %s", tools, tool)
		}
	}
}

func TestDetectLaravelMinitestWithoutDeclaredTestScript(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"composer.json":              `{"require":{"laravel/framework":"^11.0"}}`,
		"artisan":                    "#!/usr/bin/env php\n",
		"tests/Unit/ExampleTest.php": "<?php\nclass ExampleTest {}\n",
	})

	commands := commandsByName(result)
	assertCommand(t, commands["test"], "php artisan test", plan.CommandInferred, plan.CapabilityTestRun)
	assertCommand(t, commands["server"], "php artisan serve", plan.CommandInferred, plan.CapabilityApplicationRun)
}

func TestDetectLaravelDependencyWithoutApplicationDoesNotInferServer(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"composer.json":        `{"require":{"laravel/framework":"^11.0"}}`,
		"tests/WidgetTest.php": "<?php\nclass WidgetTest {}\n",
	})

	commands := commandsByName(result)
	if _, ok := commands["server"]; ok {
		t.Fatal("Laravel dependency without application evidence unexpectedly has a server command")
	}
	assertCommand(t, commands["test"], "vendor/bin/phpunit", plan.CommandInferred, plan.CapabilityTestRun)
}

func TestDetectComposerLibraryPHPUnitAndPest(t *testing.T) {
	t.Parallel()

	phpunit := detectFiles(t, map[string]string{
		"composer.json":        `{"require":{"php":"^8.2"},"require-dev":{"phpunit/phpunit":"^11.0"}}`,
		"phpunit.xml.dist":     "<phpunit></phpunit>\n",
		"tests/ParserTest.php": "<?php\nclass ParserTest {}\n",
	})
	assertCommand(t, commandsByName(phpunit)["test"], "vendor/bin/phpunit", plan.CommandInferred, plan.CapabilityTestRun)
	if _, ok := commandsByName(phpunit)["server"]; ok {
		t.Fatal("Composer library unexpectedly has a server command")
	}

	pest := detectFiles(t, map[string]string{
		"composer.json":     `{"require-dev":{"pestphp/pest":"^3.0"}}`,
		"tests/Pest.php":    "<?php\n",
		"tests/example.php": "<?php\nit('works');\n",
	})
	assertCommand(t, commandsByName(pest)["test"], "vendor/bin/pest", plan.CommandInferred, plan.CapabilityTestRun)
}

func TestDetectSymfonyFrameworkWithoutInferredServer(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"composer.json":            `{"require":{"php":"^8.3","symfony/framework-bundle":"^7.2"}}`,
		"bin/console":              "#!/usr/bin/env php\n",
		"phpunit.xml.dist":         "<phpunit></phpunit>\n",
		"tests/ControllerTest.php": "<?php\nclass ControllerTest {}\n",
	})

	if !hasProperty(result, plan.PropertyFramework, "symfony") {
		t.Fatalf("missing Symfony framework in %+v", result.Findings)
	}
	commands := commandsByName(result)
	assertCommand(t, commands["test"], "vendor/bin/phpunit", plan.CommandInferred, plan.CapabilityTestRun)
	if _, ok := commands["server"]; ok {
		t.Fatal("Symfony project unexpectedly has an inferred server command")
	}
}

func TestDetectReportsConflictingRuntimePins(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"composer.json":  `{}`,
		".php-version":   "8.3.6\n",
		".tool-versions": "php 8.4.1\n",
	})

	if len(result.Conflicts) != 1 || result.Conflicts[0].Subject != "runtime.php.version" {
		t.Fatalf("conflicts = %+v, want one PHP runtime conflict", result.Conflicts)
	}
	if !hasRuntime(result, "8.3.6") || !hasRuntime(result, "8.4.1") {
		t.Fatalf("runtime findings = %+v, want both conflicting pins", result.Findings)
	}
}

func TestDetectMergesMatchingRuntimeEvidence(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"composer.json": `{"require":{"php":"8.3.6"}}`,
		".php-version":  "8.3.6\n",
	})

	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if !ok || item.Requirement.Name != "php" || item.Requirement.Version != "8.3.6" {
			continue
		}
		sources := make([]string, 0, len(item.Requirement.Evidence))
		for _, evidence := range item.Requirement.Evidence {
			sources = append(sources, evidence.Source)
		}
		if !slices.Contains(sources, ".php-version") || !slices.Contains(sources, "composer.json") {
			t.Fatalf("runtime evidence = %v, want .php-version and composer.json", sources)
		}
		return
	}
	t.Fatal("missing merged PHP 8.3.6 requirement")
}

func TestDetectDeclaresComposerScriptsAndSkipsEventHooks(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"composer.json": `{
  "require": {"php": ">=8.1"},
  "scripts": {
    "test": "@php vendor/bin/phpunit",
    "phpstan": "@php vendor/bin/phpstan analyse",
    "post-install-cmd": "@php -r \"echo 'hook';\""
  }
}`,
		"tests/LoggerTest.php": "<?php\nclass LoggerTest {}\n",
	})

	commands := commandsByName(result)
	assertCommand(t, commands["test"], "composer run-script test", plan.CommandDeclared, plan.CapabilityTestRun)
	assertCommand(t, commands["phpstan"], "composer run-script phpstan", plan.CommandDeclared, plan.CapabilityCodeTypecheck)
	if _, ok := commands["post-install-cmd"]; ok {
		t.Fatal("post-install-cmd event hook was emitted as a command")
	}
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

func hasPackageManager(result provider.Result, name string) bool {
	for _, finding := range result.Findings {
		item, ok := finding.(plan.PropertyFinding)
		if ok && item.Property.Kind == plan.PropertyPackageManager && item.Property.Name == name {
			return true
		}
	}
	return false
}

func hasRuntime(result provider.Result, version string) bool {
	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if ok && item.Requirement.Kind == plan.RequirementRuntime && item.Requirement.Name == "php" && item.Requirement.Version == version {
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

func assertCommand(t *testing.T, command plan.Command, run string, origin plan.CommandOrigin, capability plan.Capability) {
	t.Helper()
	if command.Run == nil || *command.Run != run || command.Origin != origin {
		t.Fatalf("command = %+v, want %s %q", command, origin, run)
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
