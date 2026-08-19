package python

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

func TestDetectReturnsNothingWithoutPythonManifest(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{"README.md": "hello\n"})
	if len(result.Findings) != 0 {
		t.Fatalf("Detect() = %+v, want no findings", result)
	}
}

func TestDetectDjangoProjectRuntimesPipToolsAndPytest(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml": `
[project]
name = "widget"
requires-python = ">=3.12"
dependencies = ["django>=5.0", "ruff"]

[project.optional-dependencies]
dev = ["pytest", "pytest-django"]

[tool.pytest.ini_options]
testpaths = ["tests"]

[tool.ruff]
line-length = 88
`,
		".python-version":      "3.12.8\n",
		".tool-versions":       "python 3.12.8\nnodejs 22.4.0\n",
		"manage.py":            "#!/usr/bin/env python\n",
		"tests/test_widget.py": "def test_widget():\n    assert True\n",
		"ruff.toml":            "line-length = 88\n",
		"requirements.txt":     "django>=5.0\n",
	})

	if !hasProperty(result, plan.PropertyLanguage, "python") {
		t.Fatalf("missing Python language in %+v", result.Findings)
	}
	if !hasProperty(result, plan.PropertyFramework, "django") {
		t.Fatalf("missing Django framework in %+v", result.Findings)
	}
	if !hasPackageManager(result, "pip") {
		t.Fatalf("missing pip in %+v", result.Findings)
	}
	for _, version := range []string{"3.12.8", ">=3.12"} {
		if !hasRuntime(result, version) {
			t.Fatalf("missing Python runtime %q in %+v", version, result.Findings)
		}
	}

	commands := commandsByName(result)
	assertCommand(t, commands["install dependencies"], "pip install -r requirements.txt -e '.[dev]'", plan.CapabilityDependenciesInstall)
	assertCommand(t, commands["test"], "pytest", plan.CapabilityTestRun)
	assertCommand(t, commands["server"], "python manage.py runserver", plan.CapabilityApplicationRun)

	tools := configuredToolValues(result)
	for _, tool := range []string{"pytest", "ruff"} {
		if !slices.Contains(tools, tool) {
			t.Fatalf("configured tools = %v, want %s", tools, tool)
		}
	}
}

func TestDetectUvLockfileSelectsUvSync(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml":    "[project]\nname = \"click\"\nrequires-python = \">=3.10\"\n",
		"uv.lock":           "version = 1\n",
		"tests/test_cli.py": "def test_cli():\n    assert True\n",
	})

	if !hasPackageManager(result, "uv") {
		t.Fatalf("missing uv in %+v", result.Findings)
	}
	assertCommand(t, commandsByName(result)["install dependencies"], "uv sync", plan.CapabilityDependenciesInstall)
	assertCommand(t, commandsByName(result)["test"], "uv run python -m unittest", plan.CapabilityTestRun)
}

func TestDetectPoetryProject(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml": `
[tool.poetry]
name = "widget"

[tool.poetry.dependencies]
python = "^3.11"
flask = "^3.0"

[tool.poetry.group.dev.dependencies]
pytest = "^8.0"
`,
		"poetry.lock":       "[[package]]\nname = \"flask\"\n",
		"app.py":            "from flask import Flask\napp = Flask(__name__)\n",
		"tests/test_app.py": "def test_app():\n    assert True\n",
	})

	if !hasProperty(result, plan.PropertyFramework, "flask") {
		t.Fatalf("missing Flask framework in %+v", result.Findings)
	}
	if !hasPackageManager(result, "poetry") {
		t.Fatalf("missing poetry in %+v", result.Findings)
	}
	if !hasRuntime(result, "^3.11") {
		t.Fatalf("missing Poetry Python pin in %+v", result.Findings)
	}
	commands := commandsByName(result)
	assertCommand(t, commands["install dependencies"], "poetry install", plan.CapabilityDependenciesInstall)
	assertCommand(t, commands["test"], "poetry run pytest", plan.CapabilityTestRun)
	assertCommand(t, commands["server"], "poetry run flask run", plan.CapabilityApplicationRun)
}

func TestDetectFlaskDependencyWithoutAppDoesNotInferServer(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml": "[project]\nname = \"flask\"\ndependencies = [\"flask\"]\n",
	})

	if _, ok := commandsByName(result)["server"]; ok {
		t.Fatal("Flask dependency without application evidence unexpectedly has a server command")
	}
}

func TestDetectDjangoDependencyWithoutManagePyDoesNotInferServer(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml": "[project]\nname = \"djangorestframework\"\ndependencies = [\"django\"]\n",
	})

	if _, ok := commandsByName(result)["server"]; ok {
		t.Fatal("Django dependency without manage.py unexpectedly has a server command")
	}
}

func TestDetectUnittestWithoutPytest(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"setup.py":             "from setuptools import setup\nsetup(name=\"widget\")\n",
		"tests/test_widget.py": "import unittest\nclass TestWidget(unittest.TestCase):\n    pass\n",
	})

	assertCommand(t, commandsByName(result)["test"], "python -m unittest", plan.CapabilityTestRun)
	assertCommand(t, commandsByName(result)["install dependencies"], "pip install -e .", plan.CapabilityDependenciesInstall)
}

func TestDetectMergesSetupPyWithBuildSystemOnlyPyproject(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml": "[build-system]\nrequires = [\"setuptools\"]\nbuild-backend = \"setuptools.build_meta\"\n",
		"setup.py":       "from setuptools import setup\nsetup(name=\"widget\", install_requires=[\"django\"])\n",
		"manage.py":      "#!/usr/bin/env python\n",
		"tests.py":       "from django.test import TestCase\n",
	})

	if !hasProperty(result, plan.PropertyFramework, "django") {
		t.Fatalf("missing Django from setup.py in %+v", result.Findings)
	}
	assertCommand(t, commandsByName(result)["install dependencies"], "pip install -e .", plan.CapabilityDependenciesInstall)
	assertCommand(t, commandsByName(result)["test"], "python manage.py test", plan.CapabilityTestRun)
}

func TestDetectMergesPipfileWhenPyprojectIsToolOnly(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml": "[tool.ruff]\nline-length = 88\n",
		"Pipfile": `
[packages]
django = "*"

[dev-packages]
pytest = "*"

[requires]
python_version = "3.12"
`,
		"manage.py":            "#!/usr/bin/env python\n",
		"tests/test_widget.py": "def test_widget():\n    assert True\n",
	})

	if !hasPackageManager(result, "pipenv") {
		t.Fatalf("missing pipenv in %+v", result.Findings)
	}
	if !hasProperty(result, plan.PropertyFramework, "django") {
		t.Fatalf("missing Django from Pipfile in %+v", result.Findings)
	}
	if !hasRuntime(result, "3.12") {
		t.Fatalf("missing Pipfile Python pin in %+v", result.Findings)
	}
	sources := requirementSources(result, "python", "3.12")
	if !slices.Contains(sources, "Pipfile") {
		t.Fatalf("Python 3.12 evidence sources = %v, want Pipfile", sources)
	}
	if slices.Contains(sources, "pyproject.toml") {
		t.Fatalf("Python 3.12 evidence sources = %v, did not want pyproject.toml", sources)
	}
	install := commandsByName(result)["install dependencies"]
	assertCommand(t, install, "pipenv install --dev", plan.CapabilityDependenciesInstall)
	installSources := commandEvidenceSources(install)
	if !slices.Contains(installSources, "Pipfile") {
		t.Fatalf("install evidence sources = %v, want Pipfile", installSources)
	}
	assertCommand(t, commandsByName(result)["test"], "pipenv run pytest", plan.CapabilityTestRun)
}

func TestDetectIgnoresVirtualEnvMarkerTests(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml":     "[project]\nname = \"widget\"\n",
		"env/pyvenv.cfg":     "home = /usr/bin\n",
		"env/test_leaked.py": "def test_leaked():\n    assert True\n",
	})

	if _, ok := commandsByName(result)["test"]; ok {
		t.Fatal("virtualenv tests unexpectedly produced a test command")
	}
}

func TestDetectIgnoresVirtualenvNamedEnv(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml": "[project]\nname = \"widget\"\n",
		"env/lib/python3.12/site-packages/other/test_other.py": "def test_other():\n    assert True\n",
	})

	if _, ok := commandsByName(result)["test"]; ok {
		t.Fatal("virtualenv tests unexpectedly produced a test command")
	}
}

func TestDetectPrefersPytestTestpathsOverFilenameMatch(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml": `
[project]
name = "widget"
dependencies = ["pytest"]

[tool.pytest.ini_options]
testpaths = ["src/tests"]
`,
		"src/app/settings/test_settings.py": "DEBUG = True\n",
		"src/tests/test_widget.py":          "def test_widget():\n    assert True\n",
	})

	sources := commandEvidenceSources(commandsByName(result)["test"])
	if !slices.Contains(sources, "src/tests/test_widget.py") {
		t.Fatalf("test evidence = %v, want src/tests/test_widget.py", sources)
	}
	if slices.Contains(sources, "src/app/settings/test_settings.py") {
		t.Fatalf("test evidence = %v, did not want test_settings.py", sources)
	}
}

func TestDetectPrefersTestsDirectoryOverFilenameMatch(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml": `
[project]
name = "widget"
dependencies = ["pytest"]
`,
		"src/app/settings/test_settings.py": "DEBUG = True\n",
		"src/tests/test_widget.py":          "def test_widget():\n    assert True\n",
	})

	sources := commandEvidenceSources(commandsByName(result)["test"])
	if !slices.Contains(sources, "src/tests/test_widget.py") {
		t.Fatalf("test evidence = %v, want src/tests/test_widget.py", sources)
	}
	if slices.Contains(sources, "src/app/settings/test_settings.py") {
		t.Fatalf("test evidence = %v, did not want test_settings.py", sources)
	}
}

func TestDetectReadsPytestIniTestpaths(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml":                    "[project]\nname = \"widget\"\ndependencies = [\"pytest\"]\n",
		"pytest.ini":                        "[pytest]\ntestpaths = integration\n",
		"src/app/settings/test_settings.py": "DEBUG = True\n",
		"tests/test_widget.py":              "def test_widget():\n    assert True\n",
		"integration/test_api.py":           "def test_api():\n    assert True\n",
	})

	sources := commandEvidenceSources(commandsByName(result)["test"])
	if !slices.Contains(sources, "integration/test_api.py") {
		t.Fatalf("test evidence = %v, want integration/test_api.py", sources)
	}
	if slices.Contains(sources, "tests/test_widget.py") {
		t.Fatalf("test evidence = %v, did not want the conventional tests/ file when pytest.ini names integration", sources)
	}
}

func TestDetectFallsBackToFilenameTestMatch(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml":                    "[project]\nname = \"widget\"\ndependencies = [\"pytest\"]\n",
		"src/app/settings/test_settings.py": "DEBUG = True\n",
	})

	sources := commandEvidenceSources(commandsByName(result)["test"])
	if !slices.Contains(sources, "src/app/settings/test_settings.py") {
		t.Fatalf("test evidence = %v, want filename fallback", sources)
	}
}

func TestDetectCompetingLockfiles(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml": "[project]\nname = \"widget\"\n",
		"poetry.lock":    "[[package]]\n",
		"uv.lock":        "version = 1\n",
	})

	if len(result.Ambiguities) != 1 || result.Ambiguities[0].Subject != "tool.package-manager" {
		t.Fatalf("ambiguities = %+v, want one package-manager ambiguity", result.Ambiguities)
	}
	if !strings.Contains(result.Ambiguities[0].Message, "lockfiles") {
		t.Fatalf("ambiguity = %q, want lockfiles wording", result.Ambiguities[0].Message)
	}
	if _, ok := commandsByName(result)["install dependencies"]; ok {
		t.Fatal("competing lockfiles unexpectedly selected an install command")
	}
}

func TestDetectCompetingManagerSignalsDoNotClaimLockfiles(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml": `
[tool.poetry]
name = "widget"
`,
		"uv.lock": "version = 1\n",
	})

	if len(result.Ambiguities) != 1 || result.Ambiguities[0].Subject != "tool.package-manager" {
		t.Fatalf("ambiguities = %+v, want one package-manager ambiguity", result.Ambiguities)
	}
	if strings.Contains(result.Ambiguities[0].Message, "lockfiles") {
		t.Fatalf("ambiguity = %q, did not want lockfiles wording for a table signal", result.Ambiguities[0].Message)
	}
	if !strings.Contains(result.Ambiguities[0].Message, "package-manager signals") {
		t.Fatalf("ambiguity = %q, want package-manager signals wording", result.Ambiguities[0].Message)
	}
}

func TestDetectReportsConflictingRuntimePins(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml":  "[project]\nname = \"widget\"\n",
		".python-version": "3.12.8\n",
		".tool-versions":  "python 3.13.1\n",
	})

	if len(result.Conflicts) != 1 || result.Conflicts[0].Subject != "runtime.python.version" {
		t.Fatalf("conflicts = %+v, want one Python runtime conflict", result.Conflicts)
	}
	if !hasRuntime(result, "3.12.8") || !hasRuntime(result, "3.13.1") {
		t.Fatalf("runtime findings = %+v, want both conflicting pins", result.Findings)
	}
}

func TestDetectMergesMatchingRuntimeEvidence(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml":  "[project]\nname = \"widget\"\nrequires-python = \"3.12.8\"\n",
		".python-version": "3.12.8\n",
	})

	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if !ok || item.Requirement.Name != "python" || item.Requirement.Version != "3.12.8" {
			continue
		}
		sources := make([]string, 0, len(item.Requirement.Evidence))
		for _, evidence := range item.Requirement.Evidence {
			sources = append(sources, evidence.Source)
		}
		if !slices.Contains(sources, ".python-version") || !slices.Contains(sources, "pyproject.toml") {
			t.Fatalf("runtime evidence = %v, want .python-version and pyproject.toml", sources)
		}
		return
	}
	t.Fatal("missing merged Python 3.12.8 requirement")
}

func TestDetectPipfile(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"Pipfile": `
[[source]]
url = "https://pypi.org/simple"

[packages]
requests = "*"

[dev-packages]
pytest = "*"

[requires]
python_version = "3.12"
`,
		"tests/test_client.py": "def test_client():\n    assert True\n",
	})

	if !hasPackageManager(result, "pipenv") {
		t.Fatalf("missing pipenv in %+v", result.Findings)
	}
	if !hasRuntime(result, "3.12") {
		t.Fatalf("missing Pipfile Python pin in %+v", result.Findings)
	}
	assertCommand(t, commandsByName(result)["install dependencies"], "pipenv install --dev", plan.CapabilityDependenciesInstall)
	assertCommand(t, commandsByName(result)["test"], "pipenv run pytest", plan.CapabilityTestRun)
}

func TestParsePyprojectDottedProjectKeys(t *testing.T) {
	t.Parallel()

	parsed := parsePyproject(`
project.name = "widget"
project.requires-python = ">=3.12"
project.dependencies = ["django>=5.0", "pytest"]
`)
	if !parsed.HasProjectTable {
		t.Fatal("dotted project keys did not mark a project table")
	}
	if parsed.RequiresPython != ">=3.12" {
		t.Fatalf("requires-python = %q, want >=3.12", parsed.RequiresPython)
	}
	if !hasDependency(parsed, "django") || !hasDependency(parsed, "pytest") {
		t.Fatalf("dependencies = %+v, want django and pytest", parsed.Dependencies)
	}

	nested := parsePyproject(`
[project]
name = "widget"
optional-dependencies.dev = ["pytest"]
`)
	if !hasDependency(nested, "pytest") {
		t.Fatalf("dotted optional-dependencies = %+v, want pytest", nested.Dependencies)
	}
}

func TestDetectDottedProjectKeysInferInstallAndPytest(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml": `
project.name = "widget"
project.requires-python = ">=3.12"
project.dependencies = ["django>=5.0", "pytest"]
`,
		"manage.py":            "#!/usr/bin/env python\n",
		"tests/test_widget.py": "def test_widget():\n    assert True\n",
	})

	if !hasProperty(result, plan.PropertyFramework, "django") {
		t.Fatalf("missing Django from dotted project.dependencies in %+v", result.Findings)
	}
	if !hasRuntime(result, ">=3.12") {
		t.Fatalf("missing requires-python from dotted keys in %+v", result.Findings)
	}
	assertCommand(t, commandsByName(result)["install dependencies"], "pip install -e .", plan.CapabilityDependenciesInstall)
	assertCommand(t, commandsByName(result)["test"], "pytest", plan.CapabilityTestRun)
}

func TestParsePyprojectExtractsDjangoExtras(t *testing.T) {
	t.Parallel()

	parsed := parsePyproject(`
[project]
dependencies = ["Django[argon2]~=6.0.0", "djangorestframework~=3.18.0"]
[project.optional-dependencies]
dev = ["pytest", "ruff"]
`)
	if !hasDependency(parsed, "django") {
		t.Fatalf("dependencies = %+v, want django", parsed.Dependencies)
	}
	if !hasDependency(parsed, "pytest") || !hasDependency(parsed, "ruff") {
		t.Fatalf("optional dependencies = %+v, want pytest and ruff", parsed.Dependencies)
	}
}

func TestDetectDjangoFromRequirementsCitesRequirements(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml":   "[project]\nname = \"widget\"\n",
		"requirements.txt": "django>=5.0\n",
		"manage.py":        "#!/usr/bin/env python\n",
	})

	if !hasProperty(result, plan.PropertyFramework, "django") {
		t.Fatalf("missing Django framework in %+v", result.Findings)
	}
	sources := propertySources(result, plan.PropertyFramework, "django")
	if !slices.Contains(sources, "requirements.txt") {
		t.Fatalf("Django evidence sources = %v, want requirements.txt", sources)
	}
	if slices.Contains(sources, "pyproject.toml") {
		t.Fatalf("Django evidence sources = %v, did not want pyproject.toml", sources)
	}
}

func TestDetectSetupPyDescriptionIsNotDjango(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"setup.py":  "from setuptools import setup\nsetup(name=\"widget\", description=\"Django helper\")\n",
		"manage.py": "#!/usr/bin/env python\n",
		"tests.py":  "import unittest\n",
	})

	if hasProperty(result, plan.PropertyFramework, "django") {
		t.Fatalf("description literal unexpectedly produced Django in %+v", result.Findings)
	}
	if commands := commandsByName(result); commands["test"].Run != nil && *commands["test"].Run == "python manage.py test" {
		t.Fatal("setup.py description unexpectedly inferred Django tests")
	}
}

func TestParseSetupPyPreservesExtrasRequireGroup(t *testing.T) {
	t.Parallel()

	parsed := parseSetupPy(`
from setuptools import setup
setup(
    name="widget",
    extras_require={"test": ["pytest"]},
)
`)
	dep, ok := parsed.Dependencies["pytest"]
	if !ok {
		t.Fatalf("dependencies = %+v, want pytest from extras_require", parsed.Dependencies)
	}
	if len(dep.Origins) != 1 || dep.Origins[0] != (depOrigin{Kind: depKindExtra, Group: "test"}) {
		t.Fatalf("origins = %+v, want extra test", dep.Origins)
	}
}

func TestParseSetupPyDoesNotTreatTestsRequireAsMain(t *testing.T) {
	t.Parallel()

	parsed := parseSetupPy(`
from setuptools import setup
setup(
    name="widget",
    tests_require=["pytest"],
)
`)
	dep, ok := parsed.Dependencies["pytest"]
	if !ok {
		t.Fatalf("dependencies = %+v, want pytest from tests_require", parsed.Dependencies)
	}
	for _, origin := range dep.Origins {
		if origin.Kind == depKindMain {
			t.Fatalf("origins = %+v, did not want tests_require as main", dep.Origins)
		}
	}
}

func TestDetectSetupPyExtrasRequireInstallsExtra(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"setup.py": `from setuptools import setup
setup(name="widget", extras_require={"test": ["pytest"]})
`,
		"tests/test_widget.py": "def test_widget():\n    assert True\n",
	})

	assertCommand(t, commandsByName(result)["install dependencies"], "pip install -e '.[test]'", plan.CapabilityDependenciesInstall)
	assertCommand(t, commandsByName(result)["test"], "pytest", plan.CapabilityTestRun)
}

func TestDetectSetupPyTestsRequireDoesNotClaimPytest(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"setup.py": `from setuptools import setup
setup(name="widget", tests_require=["pytest"])
`,
		"tests/test_widget.py": "def test_widget():\n    assert True\n",
	})

	assertCommand(t, commandsByName(result)["install dependencies"], "pip install -e .", plan.CapabilityDependenciesInstall)
	assertCommand(t, commandsByName(result)["test"], "python -m unittest", plan.CapabilityTestRun)
}

func TestParseSetupPyIgnoresNonDependencyLiterals(t *testing.T) {
	t.Parallel()

	parsed := parseSetupPy(`
from setuptools import setup
setup(
    name="widget",
    description="Django helper",
    extras_require={"django": ["pytest"]},
)
`)
	if hasDependency(parsed, "django") {
		t.Fatalf("dependencies = %+v, did not want django from description or extra name", parsed.Dependencies)
	}
	if !hasDependency(parsed, "pytest") {
		t.Fatalf("dependencies = %+v, want pytest from extras_require", parsed.Dependencies)
	}
}

func TestDetectPoetryTableWithoutLockfileIgnoresRequirements(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml": `
[tool.poetry]
name = "widget"

[tool.poetry.dependencies]
python = "^3.11"
`,
		"requirements.txt": "requests\n",
	})

	if !hasPackageManager(result, "poetry") {
		t.Fatalf("missing poetry in %+v", result.Findings)
	}
	if hasPackageManager(result, "pip") {
		t.Fatalf("explicit poetry table unexpectedly added pip in %+v", result.Findings)
	}
	if len(result.Ambiguities) != 0 {
		t.Fatalf("ambiguities = %+v, want none", result.Ambiguities)
	}
	assertCommand(t, commandsByName(result)["install dependencies"], "poetry install", plan.CapabilityDependenciesInstall)
}

func TestDetectPoetryDoesNotTreatRequirementsPytestAsInstalled(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml": `
[tool.poetry]
name = "widget"

[tool.poetry.dependencies]
python = "^3.11"
`,
		"requirements.txt":     "pytest\n",
		"tests/test_widget.py": "def test_widget():\n    assert True\n",
	})

	assertCommand(t, commandsByName(result)["install dependencies"], "poetry install", plan.CapabilityDependenciesInstall)
	assertCommand(t, commandsByName(result)["test"], "poetry run python -m unittest", plan.CapabilityTestRun)
}

func TestDetectUvDoesNotTreatRequirementsPytestAsInstalled(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml": `
[project]
name = "widget"

[tool.uv]
`,
		"uv.lock":              "version = 1\n",
		"requirements.txt":     "pytest\n",
		"tests/test_widget.py": "def test_widget():\n    assert True\n",
	})

	assertCommand(t, commandsByName(result)["install dependencies"], "uv sync", plan.CapabilityDependenciesInstall)
	assertCommand(t, commandsByName(result)["test"], "uv run python -m unittest", plan.CapabilityTestRun)
}

func TestDetectPipStillRunsRequirementsPytest(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml":       "[project]\nname = \"widget\"\n",
		"requirements.txt":     "pytest\n",
		"tests/test_widget.py": "def test_widget():\n    assert True\n",
	})

	assertCommand(t, commandsByName(result)["install dependencies"], "pip install -r requirements.txt", plan.CapabilityDependenciesInstall)
	assertCommand(t, commandsByName(result)["test"], "pytest", plan.CapabilityTestRun)
}

func TestDetectPoetryDoesNotTreatRequirementsDjangoAsInstalled(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml": `
[tool.poetry]
name = "widget"

[tool.poetry.dependencies]
python = "^3.11"
`,
		"requirements.txt": "django>=5.0\n",
		"manage.py":        "#!/usr/bin/env python\n",
		"tests.py":         "import unittest\n",
	})

	assertCommand(t, commandsByName(result)["test"], "poetry run python -m unittest", plan.CapabilityTestRun)
	if _, ok := commandsByName(result)["server"]; ok {
		t.Fatal("requirements-only Django unexpectedly inferred a Poetry server command")
	}
}

func TestDetectDjangoTestsPyEmitsManagePyTest(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml": "[project]\nname = \"widget\"\ndependencies = [\"django\"]\n",
		"manage.py":      "#!/usr/bin/env python\n",
		"tests.py":       "from django.test import TestCase\n",
	})

	assertCommand(t, commandsByName(result)["test"], "python manage.py test", plan.CapabilityTestRun)
}

func TestDetectDoesNotTreatTestingModuleAsTests(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml": "[project]\nname = \"widget\"\ndependencies = [\"django\"]\n",
		"manage.py":      "#!/usr/bin/env python\n",
		"testing.py":     "def helper():\n    pass\n",
	})

	if _, ok := commandsByName(result)["test"]; ok {
		t.Fatal("testing.py unexpectedly produced a test command")
	}
}

func TestDetectToolOnlyPyprojectDoesNotInferInstall(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml": "[tool.ruff]\nline-length = 88\n",
	})

	if _, ok := commandsByName(result)["install dependencies"]; ok {
		t.Fatal("configuration-only pyproject unexpectedly inferred pip install -e .")
	}
	if hasPackageManager(result, "pip") {
		t.Fatalf("configuration-only pyproject unexpectedly selected pip in %+v", result.Findings)
	}
	if !slices.Contains(configuredToolValues(result), "ruff") {
		t.Fatalf("configured tools = %v, want ruff", configuredToolValues(result))
	}
}

func TestDetectPDMDevDependenciesSelectPytest(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml": `
[project]
name = "widget"

[tool.pdm.dev-dependencies]
test = ["pytest", "ruff"]
`,
		"pdm.lock":             "[[package]]\n",
		"tests/test_widget.py": "def test_widget():\n    assert True\n",
	})

	if !hasPackageManager(result, "pdm") {
		t.Fatalf("missing pdm in %+v", result.Findings)
	}
	assertCommand(t, commandsByName(result)["test"], "pdm run pytest", plan.CapabilityTestRun)
	tools := configuredToolValues(result)
	for _, tool := range []string{"pytest", "ruff"} {
		if !slices.Contains(tools, tool) {
			t.Fatalf("configured tools = %v, want %s", tools, tool)
		}
	}
}

func TestDetectMypyExtensionsIsNotConfiguredMypy(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml": "[project]\nname = \"widget\"\ndependencies = [\"mypy-extensions\"]\n",
	})

	if slices.Contains(configuredToolValues(result), "mypy") {
		t.Fatalf("mypy-extensions unexpectedly configured mypy in %+v", result.Findings)
	}
}

func TestDetectOptionalPytestExtraUsesInstallExtra(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml": `
[project]
name = "widget"
dependencies = ["django"]

[project.optional-dependencies]
dev = ["pytest"]
`,
		"tests/test_widget.py": "def test_widget():\n    assert True\n",
	})

	assertCommand(t, commandsByName(result)["install dependencies"], "pip install -e '.[dev]'", plan.CapabilityDependenciesInstall)
	assertCommand(t, commandsByName(result)["test"], "pytest", plan.CapabilityTestRun)
}

func TestDetectUvOptionalPytestUsesSyncExtra(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml": `
[project]
name = "widget"

[project.optional-dependencies]
dev = ["pytest"]
`,
		"uv.lock":              "version = 1\n",
		"tests/test_widget.py": "def test_widget():\n    assert True\n",
	})

	assertCommand(t, commandsByName(result)["install dependencies"], "uv sync --extra dev", plan.CapabilityDependenciesInstall)
	assertCommand(t, commandsByName(result)["test"], "uv run pytest", plan.CapabilityTestRun)
}

func TestDetectUvDependencyGroupUsesSyncGroup(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml": `
[project]
name = "widget"

[dependency-groups]
tests = ["pytest"]
`,
		"uv.lock":              "version = 1\n",
		"tests/test_widget.py": "def test_widget():\n    assert True\n",
	})

	assertCommand(t, commandsByName(result)["install dependencies"], "uv sync --group tests", plan.CapabilityDependenciesInstall)
}

func TestDetectUvDefaultGroupDoesNotAddSyncGroup(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml": `
[project]
name = "widget"

[dependency-groups]
dev = ["pytest"]
tests = ["pytest"]
tests-random = ["pytest-randomly"]

[tool.uv]
default-groups = ["dev", "tests"]
`,
		"uv.lock":              "version = 1\n",
		"tests/test_widget.py": "def test_widget():\n    assert True\n",
	})

	assertCommand(t, commandsByName(result)["install dependencies"], "uv sync", plan.CapabilityDependenciesInstall)
}

func TestDetectRequirementsPlusOptionalPytestKeepsExtra(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml": `
[project]
name = "widget"

[project.optional-dependencies]
dev = ["pytest"]
`,
		"requirements.txt":     "django>=5.0\n",
		"tests/test_widget.py": "def test_widget():\n    assert True\n",
	})

	assertCommand(t, commandsByName(result)["install dependencies"], "pip install -r requirements.txt -e '.[dev]'", plan.CapabilityDependenciesInstall)
	assertCommand(t, commandsByName(result)["test"], "pytest", plan.CapabilityTestRun)
}

func TestDetectOptionalPoetryGroupUsesWith(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml": `
[tool.poetry]
name = "widget"

[tool.poetry.dependencies]
python = "^3.11"

[tool.poetry.group.test]
optional = true

[tool.poetry.group.test.dependencies]
pytest = "^8.0"
`,
		"poetry.lock":          "[[package]]\n",
		"tests/test_widget.py": "def test_widget():\n    assert True\n",
	})

	assertCommand(t, commandsByName(result)["install dependencies"], "poetry install --with test", plan.CapabilityDependenciesInstall)
	assertCommand(t, commandsByName(result)["test"], "poetry run pytest", plan.CapabilityTestRun)
}

func TestDetectPoetryOptionalExtraUsesExtrasFlag(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml": `
[tool.poetry]
name = "widget"

[tool.poetry.dependencies]
python = "^3.11"
pytest = {version = "^8", optional = true}

[tool.poetry.extras]
test = ["pytest"]
`,
		"poetry.lock":          "[[package]]\n",
		"tests/test_widget.py": "def test_widget():\n    assert True\n",
	})

	assertCommand(t, commandsByName(result)["install dependencies"], "poetry install --extras test", plan.CapabilityDependenciesInstall)
	assertCommand(t, commandsByName(result)["test"], "poetry run pytest", plan.CapabilityTestRun)
}

func TestParsePoetryOptionalExtraMembership(t *testing.T) {
	t.Parallel()

	parsed := parsePyproject(`
[tool.poetry.dependencies]
python = "^3.11"
pytest = {version = "^8", optional = true}

[tool.poetry.extras]
test = ["pytest"]
`)
	dep, ok := parsed.Dependencies["pytest"]
	if !ok {
		t.Fatalf("dependencies = %+v, want pytest", parsed.Dependencies)
	}
	for _, origin := range dep.Origins {
		if origin.Kind == depKindMain {
			t.Fatalf("origins = %+v, did not want optional pytest as main", dep.Origins)
		}
	}
	found := false
	for _, origin := range dep.Origins {
		if origin.Kind == depKindExtra && origin.Group == "test" {
			found = true
		}
	}
	if !found {
		t.Fatalf("origins = %+v, want extra test", dep.Origins)
	}
}

func TestDetectUvLegacyDevDependenciesSelectPytest(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml": `
[project]
name = "widget"

[tool.uv]
dev-dependencies = ["pytest"]
`,
		"uv.lock":              "version = 1\n",
		"tests/test_widget.py": "def test_widget():\n    assert True\n",
	})

	assertCommand(t, commandsByName(result)["install dependencies"], "uv sync", plan.CapabilityDependenciesInstall)
	assertCommand(t, commandsByName(result)["test"], "uv run pytest", plan.CapabilityTestRun)
}

func TestDetectLegacyPoetryDevDependenciesSelectPytest(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml": `
[tool.poetry]
name = "widget"

[tool.poetry.dependencies]
python = "^3.11"

[tool.poetry.dev-dependencies]
pytest = "^8.0"
`,
		"poetry.lock":          "[[package]]\n",
		"tests/test_widget.py": "def test_widget():\n    assert True\n",
	})

	assertCommand(t, commandsByName(result)["test"], "poetry run pytest", plan.CapabilityTestRun)
	assertCommand(t, commandsByName(result)["install dependencies"], "poetry install", plan.CapabilityDependenciesInstall)
}

func TestDetectPDMOptionalExtraUsesGroupFlag(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml": `
[project]
name = "widget"

[project.optional-dependencies]
test = ["pytest"]

[tool.pdm]
`,
		"pdm.lock":             "[[package]]\n",
		"tests/test_widget.py": "def test_widget():\n    assert True\n",
	})

	assertCommand(t, commandsByName(result)["install dependencies"], "pdm install -G test", plan.CapabilityDependenciesInstall)
	assertCommand(t, commandsByName(result)["test"], "pdm run pytest", plan.CapabilityTestRun)
}

func TestDetectSetupCfgPytestInfersPytest(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml": "[project]\nname = \"widget\"\n",
		"setup.cfg": `[tool:pytest]
testpaths = tests
`,
		"tests/test_widget.py": "def test_widget():\n    assert True\n",
	})

	assertCommand(t, commandsByName(result)["test"], "pytest", plan.CapabilityTestRun)
	if !slices.Contains(configuredToolValues(result), "pytest") {
		t.Fatalf("configured tools = %v, want pytest", configuredToolValues(result))
	}
}

func TestDetectSetupCfgSectionsConfigureSupportedTools(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml": "[project]\nname = \"widget\"\n",
		"setup.cfg": `[flake8]
max-line-length = 88
[mypy]
python_version = 3.12
[isort]
profile = black
[pylint]
max-line-length = 88
`,
	})

	tools := configuredToolValues(result)
	for _, tool := range []string{"flake8", "mypy", "isort", "pylint"} {
		if !slices.Contains(tools, tool) {
			t.Fatalf("configured tools = %v, want %s from setup.cfg", tools, tool)
		}
		sources := factSources(result, "tool.configured", tool)
		if !slices.Contains(sources, "setup.cfg") {
			t.Fatalf("%s evidence sources = %v, want setup.cfg", tool, sources)
		}
	}
}

func TestDetectToxIniSectionsConfigureSupportedTools(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml": "[project]\nname = \"widget\"\n",
		"tox.ini": `[tox]
envlist = py312
[flake8]
max-line-length = 88
[mypy]
python_version = 3.12
[isort]
profile = black
`,
	})

	tools := configuredToolValues(result)
	for _, tool := range []string{"flake8", "mypy", "isort"} {
		if !slices.Contains(tools, tool) {
			t.Fatalf("configured tools = %v, want %s from tox.ini", tools, tool)
		}
		sources := factSources(result, "tool.configured", tool)
		if !slices.Contains(sources, "tox.ini") {
			t.Fatalf("%s evidence sources = %v, want tox.ini", tool, sources)
		}
	}
}

func TestDetectPytestPluginInfersPytest(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml": `
[project]
name = "widget"
dependencies = ["pytest-cov"]
`,
		"tests/test_widget.py": "def test_widget():\n    assert True\n",
	})

	test := commandsByName(result)["test"]
	assertCommand(t, test, "pytest", plan.CapabilityTestRun)
	found := false
	for _, evidence := range test.Evidence {
		if evidence.Pointer == "/dependencies/pytest-cov" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("test evidence = %+v, want /dependencies/pytest-cov", test.Evidence)
	}
}

func TestDetectPrefixedPytestCovCitesRequirements(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pyproject.toml":   "[project]\nname = \"widget\"\n",
		"requirements.txt": "pytest-cov\n",
	})

	if !slices.Contains(configuredToolValues(result), "pytest") {
		t.Fatalf("configured tools = %v, want pytest", configuredToolValues(result))
	}
	sources := factSources(result, "tool.configured", "pytest")
	if !slices.Contains(sources, "requirements.txt") {
		t.Fatalf("pytest evidence sources = %v, want requirements.txt", sources)
	}
	if slices.Contains(sources, "pyproject.toml") {
		t.Fatalf("pytest evidence sources = %v, did not want pyproject.toml", sources)
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
		if ok && item.Requirement.Kind == plan.RequirementRuntime && item.Requirement.Name == "python" && item.Requirement.Version == version {
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

func commandEvidenceSources(command plan.Command) []string {
	sources := make([]string, 0, len(command.Evidence))
	for _, evidence := range command.Evidence {
		sources = append(sources, evidence.Source)
	}
	return sources
}

func configuredToolValues(result provider.Result) []string {
	var values []string
	for _, finding := range result.Findings {
		item, ok := finding.(plan.PropertyFinding)
		if ok && item.Property.Kind == plan.PropertyFact && item.Property.Name == "tool.configured" {
			values = append(values, item.Property.Value)
		}
	}
	return values
}

func propertySources(result provider.Result, kind plan.PropertyKind, name string) []string {
	for _, finding := range result.Findings {
		item, ok := finding.(plan.PropertyFinding)
		if !ok || item.Property.Kind != kind || item.Property.Name != name {
			continue
		}
		sources := make([]string, 0, len(item.Property.Evidence))
		for _, evidence := range item.Property.Evidence {
			sources = append(sources, evidence.Source)
		}
		return sources
	}
	return nil
}

func requirementSources(result provider.Result, name, version string) []string {
	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if !ok || item.Requirement.Name != name || item.Requirement.Version != version {
			continue
		}
		sources := make([]string, 0, len(item.Requirement.Evidence))
		for _, evidence := range item.Requirement.Evidence {
			sources = append(sources, evidence.Source)
		}
		return sources
	}
	return nil
}

func factSources(result provider.Result, name, value string) []string {
	for _, finding := range result.Findings {
		item, ok := finding.(plan.PropertyFinding)
		if !ok || item.Property.Kind != plan.PropertyFact || item.Property.Name != name || item.Property.Value != value {
			continue
		}
		sources := make([]string, 0, len(item.Property.Evidence))
		for _, evidence := range item.Property.Evidence {
			sources = append(sources, evidence.Source)
		}
		return sources
	}
	return nil
}
