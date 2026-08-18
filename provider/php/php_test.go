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

func TestDetectLaravelPackageWithoutArtisanDoesNotInferArtisanCommands(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"composer.json":        `{"require":{"laravel/framework":"^11.0"}}`,
		"bootstrap/app.php":    "<?php\n",
		"config/app.php":       "<?php\n",
		"tests/WidgetTest.php": "<?php\nclass WidgetTest {}\n",
	})

	commands := commandsByName(result)
	if _, ok := commands["server"]; ok {
		t.Fatal("Laravel package without artisan unexpectedly has a server command")
	}
	if _, ok := commands["test"]; ok {
		t.Fatal("Laravel package without a PHPUnit runner unexpectedly has a test command")
	}
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
	if _, ok := commands["test"]; ok {
		t.Fatal("Laravel dependency without a PHPUnit runner unexpectedly has a test command")
	}
}

func TestDetectDoesNotInferPHPUnitFromTestFilenamesAlone(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"composer.json":        `{"require":{"php":"^8.2"}}`,
		"tests/ParserTest.php": "<?php\nclass ParserTest {}\n",
	})
	if _, ok := commandsByName(result)["test"]; ok {
		t.Fatal("inferred PHPUnit from a *Test.php file without a PHPUnit runner")
	}
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

	configured := detectFiles(t, map[string]string{
		"composer.json":     `{"require":{"php":"^8.2"}}`,
		"tests/Pest.php":    "<?php\n",
		"tests/example.php": "<?php\nit('works');\n",
	})
	assertCommand(t, commandsByName(configured)["test"], "vendor/bin/pest", plan.CommandInferred, plan.CapabilityTestRun)
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
	if _, ok := commands["test"]; ok {
		t.Fatal("Symfony project without a PHPUnit runner unexpectedly has a test command")
	}
	if _, ok := commands["server"]; ok {
		t.Fatal("Symfony project unexpectedly has an inferred server command")
	}

	phpunit := detectFiles(t, map[string]string{
		"composer.json":            `{"require":{"php":"^8.3","symfony/framework-bundle":"^7.2"},"require-dev":{"phpunit/phpunit":"^11.0"}}`,
		"bin/console":              "#!/usr/bin/env php\n",
		"phpunit.xml.dist":         "<phpunit></phpunit>\n",
		"tests/ControllerTest.php": "<?php\nclass ControllerTest {}\n",
	})
	assertCommand(t, commandsByName(phpunit)["test"], "vendor/bin/phpunit", plan.CommandInferred, plan.CapabilityTestRun)

	bridge := detectFiles(t, map[string]string{
		"composer.json":            `{"require":{"php":"^8.3","symfony/framework-bundle":"^7.2"},"require-dev":{"symfony/phpunit-bridge":"^7.2"}}`,
		"bin/console":              "#!/usr/bin/env php\n",
		"phpunit.xml.dist":         "<phpunit></phpunit>\n",
		"tests/ControllerTest.php": "<?php\nclass ControllerTest {}\n",
	})
	command := commandsByName(bridge)["test"]
	assertCommand(t, command, "vendor/bin/simple-phpunit", plan.CommandInferred, plan.CapabilityTestRun)
	if !hasEvidencePointer(command.Evidence, "/require-dev/symfony~1phpunit-bridge") {
		t.Fatalf("evidence = %+v, want phpunit-bridge declaration", command.Evidence)
	}
}

func TestComposerBinaryResolvesConfiguredDirectories(t *testing.T) {
	t.Parallel()

	tests := []struct {
		binDir    string
		vendorDir string
		want      string
	}{
		{want: "vendor/bin/phpunit"},
		{binDir: "bin", want: "bin/phpunit"},
		{vendorDir: "libs", want: "libs/bin/phpunit"},
		{binDir: "{$vendor-dir}/tools", vendorDir: "libs", want: "libs/tools/phpunit"},
		{binDir: "{$home}/bin", want: "composer exec phpunit"},
	}
	for _, tt := range tests {
		manifest := composerManifest{Config: composerConfig{}}
		if tt.binDir != "" {
			manifest.Config.BinDir = []byte(`"` + tt.binDir + `"`)
		}
		if tt.vendorDir != "" {
			manifest.Config.VendorDir = []byte(`"` + tt.vendorDir + `"`)
		}
		if got := composerBinary(manifest, "phpunit"); got != tt.want {
			t.Fatalf("composerBinary(bin=%q vendor=%q) = %q, want %q", tt.binDir, tt.vendorDir, got, tt.want)
		}
	}
}

func TestDetectHonorsComposerBinDirForInferredPHPUnit(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"composer.json": `{
  "require": {"php": "^8.2"},
  "require-dev": {"phpunit/phpunit": "^11.0"},
  "config": {"bin-dir": "bin"}
}`,
		"phpunit.xml.dist":     "<phpunit></phpunit>\n",
		"tests/ParserTest.php": "<?php\nclass ParserTest {}\n",
	})
	command := commandsByName(result)["test"]
	assertCommand(t, command, "bin/phpunit", plan.CommandInferred, plan.CapabilityTestRun)
	if !hasEvidencePointer(command.Evidence, "/config/bin-dir") {
		t.Fatalf("evidence = %+v, want /config/bin-dir", command.Evidence)
	}
}

func TestDetectHonorsComposerVendorDirForInferredPest(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"composer.json": `{
  "require-dev": {"pestphp/pest": "^3.0"},
  "config": {"vendor-dir": "libs"}
}`,
		"tests/Pest.php":    "<?php\n",
		"tests/example.php": "<?php\nit('works');\n",
	})
	command := commandsByName(result)["test"]
	assertCommand(t, command, "libs/bin/pest", plan.CommandInferred, plan.CapabilityTestRun)
	if !hasEvidencePointer(command.Evidence, "/config/vendor-dir") {
		t.Fatalf("evidence = %+v, want /config/vendor-dir", command.Evidence)
	}
}

func TestDetectFallsBackToComposerExecWhenBinDirIsUnevaluable(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"composer.json": `{
  "require-dev": {"phpunit/phpunit": "^11.0"},
  "config": {"bin-dir": "{$home}/bin"}
}`,
		"phpunit.xml.dist":     "<phpunit></phpunit>\n",
		"tests/ParserTest.php": "<?php\nclass ParserTest {}\n",
	})
	assertCommand(t, commandsByName(result)["test"], "composer exec phpunit", plan.CommandInferred, plan.CapabilityTestRun)
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

func TestDetectMergesEqualPlatformPHPEvidenceIntoRequire(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"composer.json": `{
  "require": {"php": "^8.3"},
  "config": {"platform": {"php": "^8.3"}}
}`,
	})

	var found bool
	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if !ok || item.Requirement.Name != "php" || item.Requirement.Version != "^8.3" {
			continue
		}
		found = true
		if !hasEvidencePointer(item.Requirement.Evidence, "/require/php") {
			t.Fatalf("evidence = %+v, want /require/php", item.Requirement.Evidence)
		}
		if !hasEvidencePointer(item.Requirement.Evidence, "/config/platform/php") {
			t.Fatalf("evidence = %+v, want /config/platform/php merged into the same requirement", item.Requirement.Evidence)
		}
	}
	if !found {
		t.Fatal("missing merged PHP ^8.3 requirement")
	}
	if count := runtimeCount(result, "^8.3"); count != 1 {
		t.Fatalf("found %d ^8.3 requirements, want one merged requirement", count)
	}
}

func TestDetectEmitsComposerCommaRuntimeConstraint(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"composer.json": `{"require":{"php":">=8.1,<8.3"}}`,
	})

	if !hasRuntime(result, ">=8.1,<8.3") {
		t.Fatalf("missing require.php >=8.1,<8.3 in %+v", result.Findings)
	}
}

func TestDetectAcceptsDisabledComposerPlatformPackages(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"composer.json": `{
  "require": {"php": "^8.3"},
  "config": {
    "platform": {
      "php": "8.3.0",
      "ext-xdebug": false
    }
  }
}`,
	})

	if !hasRuntime(result, "8.3.0") {
		t.Fatalf("missing platform.php 8.3.0 in %+v", result.Findings)
	}
	if !hasRuntime(result, "^8.3") {
		t.Fatalf("missing require.php ^8.3 in %+v", result.Findings)
	}
}

func TestExpandComposerAtRequiresATokenBoundary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		body string
		want string
	}{
		{body: "@php", want: "php"},
		{body: "@php artisan test", want: "php artisan test"},
		{body: "@php vendor/bin/phpunit", want: "php vendor/bin/phpunit"},
		{body: "@phpartisan test", want: "@phpartisan test"},
		{body: "@phpunit", want: "@phpunit"},
	}
	for _, tt := range tests {
		if got := expandComposerAt(tt.body); got != tt.want {
			t.Fatalf("expandComposerAt(%q) = %q, want %q", tt.body, got, tt.want)
		}
	}
}

func TestDetectDoesNotInterpretPHPPlaceholderInsideScriptAlias(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"composer.json": `{
  "scripts": {
    "test": "@phpartisan test"
  }
}`,
		"artisan": "#!/usr/bin/env php\n",
	})
	command := commandsByName(result)["test"]
	if command.Run == nil || *command.Run != "composer run-script test" || command.Origin != plan.CommandDeclared {
		t.Fatalf("command = %+v, want declared composer run-script test", command)
	}
	if len(command.Interpretations) != 0 {
		t.Fatalf("interpretations = %+v, want none for @phpartisan", command.Interpretations)
	}
}

func TestDetectInterpretsPHPCLIOptionsInDeclaredTestScript(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"composer.json": `{
  "scripts": {
    "test": "@php -d memory_limit=-1 vendor/bin/phpunit"
  }
}`,
	})

	assertCommand(t, commandsByName(result)["test"], "composer run-script test", plan.CommandDeclared, plan.CapabilityTestRun)
}

func TestDetectInterpretsArtisanAfterPHPCLIOptions(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"composer.json": `{
  "require": {"laravel/framework": "^11.0"},
  "scripts": {
    "test": "@php -d memory_limit=-1 artisan test"
  }
}`,
		"artisan": "#!/usr/bin/env php\n",
	})

	assertCommand(t, commandsByName(result)["test"], "composer run-script test", plan.CommandDeclared, plan.CapabilityTestRun)
}

func TestDetectInterpretsPHPFileOptionVendorBin(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"composer.json": `{
  "scripts": {
    "test": "@php -f vendor/bin/phpunit"
  }
}`,
	})

	assertCommand(t, commandsByName(result)["test"], "composer run-script test", plan.CommandDeclared, plan.CapabilityTestRun)
}

func TestDetectInterpretsPintTestAsStyleCheck(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"composer.json": `{
  "scripts": {
    "lint": "vendor/bin/pint --test"
  }
}`,
	})

	command := commandsByName(result)["lint"]
	assertCommand(t, command, "composer run-script lint", plan.CommandDeclared, plan.CapabilityCodeLint)
	for _, interpretation := range command.Interpretations {
		if interpretation.Capability == plan.CapabilityCodeFormat {
			t.Fatalf("pint --test interpretations = %+v, did not want code.format", command.Interpretations)
		}
	}
}

func TestDetectDoesNotInferTestsFromNestedComposerProject(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"composer.json":                        `{"require":{"php":"^8.2"}}`,
		"tests/fixtures/child/composer.json":   `{"require":{"php":"^8.2"}}`,
		"tests/fixtures/child/ExampleTest.php": "<?php\nclass ExampleTest {}\n",
	})

	if _, ok := commandsByName(result)["test"]; ok {
		t.Fatal("parent inferred a test command from a nested Composer project's tests")
	}
}

func TestDetectDoesNotInferTestsFromComposerProjectAtTestRoot(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"composer.json":         `{"require":{"php":"^8.2"}}`,
		"tests/composer.json":   `{"require":{"php":"^8.2"}}`,
		"tests/ExampleTest.php": "<?php\nclass ExampleTest {}\n",
	})

	if _, ok := commandsByName(result)["test"]; ok {
		t.Fatal("parent inferred a test command from tests/composer.json")
	}
}

func TestDetectEscapesComposerScriptKeysInPointers(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"composer.json": `{
  "scripts": {
    "test/unit": "@php vendor/bin/phpunit"
  }
}`,
	})

	command := commandsByName(result)["test/unit"]
	if command.Name != "test/unit" {
		t.Fatalf("missing test/unit command in %+v", result.Findings)
	}
	if len(command.Evidence) == 0 || command.Evidence[0].Pointer != "/scripts/test~1unit" {
		t.Fatalf("evidence pointer = %+v, want /scripts/test~1unit", command.Evidence)
	}
	for _, interpretation := range command.Interpretations {
		if len(interpretation.Evidence) == 0 || interpretation.Evidence[0].Pointer != "/scripts/test~1unit" {
			t.Fatalf("interpretation pointer = %+v, want /scripts/test~1unit", interpretation.Evidence)
		}
	}
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

func runtimeCount(result provider.Result, version string) int {
	count := 0
	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if ok && item.Requirement.Kind == plan.RequirementRuntime && item.Requirement.Name == "php" && item.Requirement.Version == version {
			count++
		}
	}
	return count
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

func hasEvidencePointer(evidence []plan.Evidence, pointer string) bool {
	for _, item := range evidence {
		if item.Source == "composer.json" && item.Pointer == pointer {
			return true
		}
	}
	return false
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
