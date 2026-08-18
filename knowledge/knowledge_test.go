package knowledge

import (
	"slices"
	"testing"

	"github.com/superplanehq/suss/plan"
)

func TestInterpretSelectsTheMostSpecificInvocation(t *testing.T) {
	t.Parallel()

	matches := Interpret(Invocation{Executable: "vite", Args: []string{"build"}})
	got := capabilities(matches)
	want := []plan.Capability{plan.CapabilityArtifactBuild}
	if !slices.Equal(got, want) {
		t.Fatalf("Interpret(vite build) = %v, want %v", got, want)
	}
}

func TestInterpretMatchesBareViteAsADevServer(t *testing.T) {
	t.Parallel()

	matches := Interpret(Invocation{Executable: "vite", Args: []string{"--host"}})
	got := capabilities(matches)
	want := []plan.Capability{plan.CapabilityApplicationRun}
	if !slices.Equal(got, want) {
		t.Fatalf("Interpret(vite --host) = %v, want %v", got, want)
	}
}

func TestInterpretLeavesUnknownExecutablesUnmatched(t *testing.T) {
	t.Parallel()

	matches := Interpret(Invocation{Executable: "rollup", Args: []string{"-c"}})
	if len(matches) != 0 {
		t.Fatalf("Interpret(rollup) = %v, want no matches", matches)
	}
}

func TestParseScriptStillSplitsPipes(t *testing.T) {
	t.Parallel()

	got := ParseScript("curl -sL https://example.com | bash")
	if len(got) != 2 || got[0].Executable != "curl" || got[1].Executable != "bash" {
		t.Fatalf("ParseScript() = %+v, want curl and bash", got)
	}
}

func TestParseStatementsKeepPipelinesDoesNotSplitPipes(t *testing.T) {
	t.Parallel()

	got := ParseStatementsKeepPipelines("curl -sL https://example.com | bash")
	if len(got) != 1 || got[0].Invocation.Executable != "curl" {
		t.Fatalf("ParseStatementsKeepPipelines() = %+v, want one curl pipeline", got)
	}
}

func TestParseStatementsHandlesComments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		script string
		want   []string
	}{
		{
			name: "apostrophe in a comment does not swallow the next command",
			script: `
# into K buckets so each upload's finalize body is small
# suite leftover
pnpm test
pnpm lint
`,
			want: []string{"pnpm test", "pnpm lint"},
		},
		{
			name:   "trailing comment is stripped from the statement",
			script: "pnpm test # run the suite",
			want:   []string{"pnpm test"},
		},
		{
			name:   "hash inside double quotes is not a comment",
			script: `echo "keep # this"`,
			want:   []string{`echo "keep # this"`},
		},
		{
			name:   "hash inside single quotes is not a comment",
			script: "echo 'keep # this'",
			want:   []string{"echo 'keep # this'"},
		},
		{
			name:   "hash inside a word is not a comment",
			script: "sed 's#/[^/]*$##'",
			want:   []string{"sed 's#/[^/]*$##'"},
		},
		{
			name:   "comment-only script yields no statements",
			script: "# only a comment\n# and another",
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := statementRaws(ParseStatementsKeepPipelines(tt.script))
			if !slices.Equal(got, tt.want) {
				t.Fatalf("ParseStatementsKeepPipelines() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseStatementsCapturesCdAndEnvNames(t *testing.T) {
	t.Parallel()

	got := ParseStatements("FOO=bar cd frontend && pnpm test --run")
	if len(got) != 2 {
		t.Fatalf("ParseStatements() len = %d, want 2", len(got))
	}
	if got[0].Chdir != "frontend" || !slices.Equal(got[0].EnvNames, []string{"FOO"}) {
		t.Fatalf("first statement = %+v, want cd frontend with FOO", got[0])
	}
	if got[1].Raw != "pnpm test --run" || got[1].Invocation.Executable != "pnpm" {
		t.Fatalf("second statement = %+v, want pnpm test --run", got[1])
	}
}

func TestParseScriptSplitsShellListsAndSkipsEnvAssignments(t *testing.T) {
	t.Parallel()

	got := ParseScript("NODE_ENV=test vitest run && tsc --noEmit && eslint src")
	want := []Invocation{
		{Executable: "vitest", Args: []string{"run"}},
		{Executable: "tsc", Args: []string{"--noEmit"}},
		{Executable: "eslint", Args: []string{"src"}},
	}
	if !slices.EqualFunc(got, want, invocationsEqual) {
		t.Fatalf("ParseScript() = %#v, want %#v", got, want)
	}
}

func TestParseScriptStripsBinWrappersAndPathPrefixes(t *testing.T) {
	t.Parallel()

	got := ParseScript("npx --yes eslint . && ./node_modules/.bin/prettier --check .")
	want := []Invocation{
		{Executable: "eslint", Args: []string{"."}},
		{Executable: "prettier", Args: []string{"--check", "."}},
	}
	if !slices.EqualFunc(got, want, invocationsEqual) {
		t.Fatalf("ParseScript() = %#v, want %#v", got, want)
	}
}

func TestParseScriptStripsCoverageWrappers(t *testing.T) {
	t.Parallel()

	got := ParseScript("c8 ava")
	want := []Invocation{{Executable: "ava"}}
	if !slices.EqualFunc(got, want, invocationsEqual) {
		t.Fatalf("ParseScript() = %#v, want %#v", got, want)
	}
}

func TestParseScriptStripsBundlerExecWrapper(t *testing.T) {
	t.Parallel()

	got := ParseScript("bundle exec rspec --format progress && bin/rails test")
	want := []Invocation{
		{Executable: "rspec", Args: []string{"--format", "progress"}},
		{Executable: "rails", Args: []string{"test"}},
	}
	if !slices.EqualFunc(got, want, invocationsEqual) {
		t.Fatalf("ParseScript() = %#v, want %#v", got, want)
	}
}

func TestInterpretMatchesRubyInvocations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		invocation Invocation
		capability plan.Capability
	}{
		{invocation: Invocation{Executable: "bundle", Args: []string{"install"}}, capability: plan.CapabilityDependenciesInstall},
		{invocation: Invocation{Executable: "rspec"}, capability: plan.CapabilityTestRun},
		{invocation: Invocation{Executable: "rails", Args: []string{"test"}}, capability: plan.CapabilityTestRun},
		{invocation: Invocation{Executable: "rails", Args: []string{"db:setup", "test"}}, capability: plan.CapabilityTestRun},
		{invocation: Invocation{Executable: "rails", Args: []string{"server"}}, capability: plan.CapabilityApplicationRun},
		{invocation: Invocation{Executable: "rake", Args: []string{"test"}}, capability: plan.CapabilityTestRun},
		{invocation: Invocation{Executable: "rake", Args: []string{"build"}}, capability: plan.CapabilityArtifactBuild},
		{invocation: Invocation{Executable: "rubocop"}, capability: plan.CapabilityCodeLint},
		{invocation: Invocation{Executable: "srb", Args: []string{"tc"}}, capability: plan.CapabilityCodeTypecheck},
	}
	for _, tt := range tests {
		matches := Interpret(tt.invocation)
		if len(matches) != 1 || matches[0].Capability != tt.capability {
			t.Fatalf("Interpret(%+v) = %+v, want %s", tt.invocation, matches, tt.capability)
		}
	}
}

func TestInterpretMatchesPHPInvocations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		invocation Invocation
		capability plan.Capability
	}{
		{invocation: Invocation{Executable: "composer", Args: []string{"install"}}, capability: plan.CapabilityDependenciesInstall},
		{invocation: Invocation{Executable: "composer", Args: []string{"i"}}, capability: plan.CapabilityDependenciesInstall},
		{invocation: Invocation{Executable: "phpunit"}, capability: plan.CapabilityTestRun},
		{invocation: Invocation{Executable: "simple-phpunit"}, capability: plan.CapabilityTestRun},
		{invocation: Invocation{Executable: "pest"}, capability: plan.CapabilityTestRun},
		{invocation: Invocation{Executable: "php", Args: []string{"artisan", "test"}}, capability: plan.CapabilityTestRun},
		{invocation: Invocation{Executable: "php", Args: []string{"artisan", "serve"}}, capability: plan.CapabilityApplicationRun},
		{invocation: Invocation{Executable: "phpstan", Args: []string{"analyse"}}, capability: plan.CapabilityCodeTypecheck},
		{invocation: Invocation{Executable: "psalm"}, capability: plan.CapabilityCodeTypecheck},
		{invocation: Invocation{Executable: "pint"}, capability: plan.CapabilityCodeFormat},
		{invocation: Invocation{Executable: "pint", Args: []string{"--test"}}, capability: plan.CapabilityCodeLint},
		{invocation: Invocation{Executable: "pint", Args: []string{"--dirty", "--test"}}, capability: plan.CapabilityCodeLint},
		{invocation: Invocation{Executable: "pint", Args: []string{"app", "--test"}}, capability: plan.CapabilityCodeLint},
		{invocation: Invocation{Executable: "php-cs-fixer", Args: []string{"fix"}}, capability: plan.CapabilityCodeFormat},
		{invocation: Invocation{Executable: "php-cs-fixer", Args: []string{"fix", "--dry-run", "--diff"}}, capability: plan.CapabilityCodeLint},
		{invocation: Invocation{Executable: "php-cs-fixer", Args: []string{"check"}}, capability: plan.CapabilityCodeLint},
		{invocation: Invocation{Executable: "phpcs"}, capability: plan.CapabilityCodeLint},
	}
	for _, tt := range tests {
		matches := Interpret(tt.invocation)
		if len(matches) != 1 || matches[0].Capability != tt.capability {
			t.Fatalf("Interpret(%+v) = %+v, want %s", tt.invocation, matches, tt.capability)
		}
	}
}

func TestInterpretLeavesPHPCsFixerInspectionCommandsUnmatched(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"describe", "psr2"}, {"list-files"}, {"list"}} {
		inv := Invocation{Executable: "php-cs-fixer", Args: args}
		if matches := Interpret(inv); len(matches) != 0 {
			t.Fatalf("Interpret(%+v) = %+v, want no fabricated code.format", inv, matches)
		}
	}
}

func TestCommandNameKeepsPHPUnitFilterValuesOutOfTheName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		inv  Invocation
		want string
	}{
		{inv: Invocation{Executable: "phpunit", Args: []string{"--", "--group", "Elasticsearch"}}, want: "phpunit"},
		{inv: Invocation{Executable: "phpunit", Args: []string{"--exclude-group", "Elasticsearch,Elastica"}}, want: "phpunit"},
		{inv: Invocation{Executable: "pest", Args: []string{"--filter", "Login"}}, want: "pest"},
		{inv: Invocation{Executable: "php", Args: []string{"artisan", "test"}}, want: "php artisan"},
		{inv: Invocation{Executable: "go", Args: []string{"test", "./..."}}, want: "go test"},
	}
	for _, tt := range tests {
		if got := CommandName(tt.inv); got != tt.want {
			t.Fatalf("CommandName(%+v) = %q, want %q", tt.inv, got, tt.want)
		}
	}
}

func TestParseScriptStripsComposerExecAndPHPVendorBin(t *testing.T) {
	t.Parallel()

	got := ParseScript("composer exec phpunit -- testdox && php vendor/bin/phpstan analyse")
	want := []Invocation{
		{Executable: "phpunit", Args: []string{"--", "testdox"}},
		{Executable: "phpstan", Args: []string{"analyse"}},
	}
	if !slices.EqualFunc(got, want, invocationsEqual) {
		t.Fatalf("ParseScript() = %#v, want %#v", got, want)
	}
}

func TestParseScriptSkipsPHPCLIOptionsBeforeVendorBin(t *testing.T) {
	t.Parallel()

	got := ParseScript("php -d memory_limit=-1 vendor/bin/phpunit && php -dmemory_limit=-1 vendor/bin/phpunit --testdox && php -n vendor/bin/pest")
	want := []Invocation{
		{Executable: "phpunit"},
		{Executable: "phpunit", Args: []string{"--testdox"}},
		{Executable: "pest"},
	}
	if !slices.EqualFunc(got, want, invocationsEqual) {
		t.Fatalf("ParseScript() = %#v, want %#v", got, want)
	}
}

func TestParseScriptStripsPHPCLIOptionsBeforeArtisan(t *testing.T) {
	t.Parallel()

	got := ParseScript("php -d memory_limit=-1 artisan test")
	want := []Invocation{{Executable: "php", Args: []string{"artisan", "test"}}}
	if !slices.EqualFunc(got, want, invocationsEqual) {
		t.Fatalf("ParseScript() = %#v, want %#v", got, want)
	}
}

func TestParseScriptPreservesPHPFileOptionTargets(t *testing.T) {
	t.Parallel()

	got := ParseScript("php -f vendor/bin/phpunit && php --file vendor/bin/phpunit --testdox && php -fvendor/bin/pest")
	want := []Invocation{
		{Executable: "phpunit"},
		{Executable: "phpunit", Args: []string{"--testdox"}},
		{Executable: "pest"},
	}
	if !slices.EqualFunc(got, want, invocationsEqual) {
		t.Fatalf("ParseScript() = %#v, want %#v", got, want)
	}
}

func TestParseStatementsKeepsComposerWorkingDirWhenUnwrappingExec(t *testing.T) {
	t.Parallel()

	got := ParseStatements("composer -d tools exec phpunit")
	if len(got) != 1 || got[0].WorkingDir != "tools" || got[0].Invocation.Executable != "phpunit" {
		t.Fatalf("ParseStatements() = %+v, want phpunit in tools", got)
	}
}

func TestParseScriptNormalizesComposerGlobalOptionsBeforeInstall(t *testing.T) {
	t.Parallel()

	got := ParseScript("composer --no-interaction install && composer -v install")
	want := []Invocation{
		{Executable: "composer", Args: []string{"install"}},
		{Executable: "composer", Args: []string{"install"}},
	}
	if !slices.EqualFunc(got, want, invocationsEqual) {
		t.Fatalf("ParseScript() = %#v, want %#v", got, want)
	}
	matches := Interpret(got[0])
	if len(matches) != 1 || matches[0].Capability != plan.CapabilityDependenciesInstall {
		t.Fatalf("Interpret(composer --no-interaction install) = %+v, want dependencies.install", matches)
	}
}

func TestParseScriptDoesNotUnwrapPHPSyntaxCheckAsVendorBin(t *testing.T) {
	t.Parallel()

	got := ParseScript("php -l vendor/bin/phpunit && php --syntax-check vendor/bin/pest && php -s vendor/bin/phpunit && php -w vendor/bin/pest")
	want := []Invocation{
		{Executable: "php", Args: []string{"-l", "vendor/bin/phpunit"}},
		{Executable: "php", Args: []string{"--syntax-check", "vendor/bin/pest"}},
		{Executable: "php", Args: []string{"-s", "vendor/bin/phpunit"}},
		{Executable: "php", Args: []string{"-w", "vendor/bin/pest"}},
	}
	if !slices.EqualFunc(got, want, invocationsEqual) {
		t.Fatalf("ParseScript() = %#v, want %#v", got, want)
	}
	for _, inv := range got {
		if matches := Interpret(inv); len(matches) != 0 {
			t.Fatalf("Interpret(%+v) = %+v, want no fabricated test.run", inv, matches)
		}
	}
}

func TestParseStatementsKeepsWorkingDirAfterComposerExec(t *testing.T) {
	t.Parallel()

	got := ParseStatements("composer exec -d tools phpunit && composer exec --working-dir tools pest")
	if len(got) != 2 {
		t.Fatalf("ParseStatements() = %+v, want two statements", got)
	}
	if got[0].WorkingDir != "tools" || got[0].Invocation.Executable != "phpunit" {
		t.Fatalf("first statement = %+v, want phpunit in tools", got[0])
	}
	if got[1].WorkingDir != "tools" || got[1].Invocation.Executable != "pest" {
		t.Fatalf("second statement = %+v, want pest in tools", got[1])
	}
}

func TestParseScriptDoesNotUnwrapPHPInlineCodeAsVendorBin(t *testing.T) {
	t.Parallel()

	got := ParseScript("php -r 'echo $argv[1];' vendor/bin/phpunit")
	if len(got) != 1 || got[0].Executable != "php" {
		t.Fatalf("ParseScript() = %#v, want php, not unwrapped phpunit", got)
	}
	if matches := Interpret(got[0]); len(matches) != 0 {
		t.Fatalf("Interpret(php -r … phpunit) = %+v, want no fabricated test.run", matches)
	}
}

func TestParseScriptUnwrapsComposerExecAfterGlobalOptions(t *testing.T) {
	t.Parallel()

	got := ParseScript("composer --no-interaction exec phpunit && composer -d tools exec pest")
	want := []Invocation{
		{Executable: "phpunit"},
		{Executable: "pest"},
	}
	if !slices.EqualFunc(got, want, invocationsEqual) {
		t.Fatalf("ParseScript() = %#v, want %#v", got, want)
	}
}

func TestClassifyManagerTreatsComposerScriptAndInstall(t *testing.T) {
	t.Parallel()

	install, ok := ClassifyManager(Invocation{Executable: "composer", Args: []string{"install", "--no-interaction"}})
	if !ok || !install.Install || install.Script != "" {
		t.Fatalf("ClassifyManager(composer install) = %+v, ok=%v", install, ok)
	}

	script, ok := ClassifyManager(Invocation{Executable: "composer", Args: []string{"run-script", "test", "--", "--parallel"}})
	if !ok || script.Script != "test" || !slices.Equal(script.Args, []string{"--", "--parallel"}) {
		t.Fatalf("ClassifyManager(composer run-script test) = %+v, ok=%v", script, ok)
	}

	bare, ok := ClassifyManager(Invocation{Executable: "composer", Args: []string{"test", "--parallel"}})
	if !ok || bare.Script != "test" || !slices.Equal(bare.Args, []string{"--parallel"}) {
		t.Fatalf("ClassifyManager(composer test) = %+v, ok=%v", bare, ok)
	}

	phpstan, ok := ClassifyManager(Invocation{Executable: "composer", Args: []string{"phpstan"}})
	if !ok || phpstan.Script != "phpstan" {
		t.Fatalf("ClassifyManager(composer phpstan) = %+v, ok=%v", phpstan, ok)
	}

	update, ok := ClassifyManager(Invocation{Executable: "composer", Args: []string{"update"}})
	if !ok || update.Install || update.Script != "" {
		t.Fatalf("ClassifyManager(composer update) = %+v, ok=%v", update, ok)
	}

	for _, alias := range []string{"upgrade", "u", "info", "rm", "uninstall", "r", "cc", "completion"} {
		got, ok := ClassifyManager(Invocation{Executable: "composer", Args: []string{alias}})
		if !ok || got.Install || got.Script != "" {
			t.Fatalf("ClassifyManager(composer %s) = %+v, ok=%v, want a builtin", alias, got, ok)
		}
	}
}

func TestParseScriptStripsPythonWrappers(t *testing.T) {
	t.Parallel()

	got := ParseScript("poetry run pytest -q && uv run --locked --group dev tox run && python -m pytest && python src/manage.py test && uv run python -m pytest && poetry run python -m pytest && pdm run pytest && pipenv run flask run")
	want := []Invocation{
		{Executable: "pytest", Args: []string{"-q"}},
		{Executable: "tox", Args: []string{"run"}},
		{Executable: "pytest"},
		{Executable: "python", Args: []string{"manage.py", "test"}},
		{Executable: "pytest"},
		{Executable: "pytest"},
		{Executable: "pytest"},
		{Executable: "flask", Args: []string{"run"}},
	}
	if !slices.EqualFunc(got, want, invocationsEqual) {
		t.Fatalf("ParseScript() = %#v, want %#v", got, want)
	}
}

func TestParseScriptCapturesPoetryPipenvPdmRunDirectory(t *testing.T) {
	t.Parallel()

	got := ParseScript(`poetry -C backend run pytest && poetry --directory=packages/api run pytest && pdm -p services/web run pytest && pipenv --python 3.12 run pytest`)
	want := []Invocation{
		{Executable: "pytest", Directory: "backend"},
		{Executable: "pytest", Directory: "packages/api"},
		{Executable: "pytest", Directory: "services/web"},
		{Executable: "pytest"},
	}
	if !slices.EqualFunc(got, want, invocationsEqual) {
		t.Fatalf("ParseScript() = %#v, want %#v", got, want)
	}
}

func TestInterpretToxAndNoxOnlyMatchUnqualifiedRuns(t *testing.T) {
	t.Parallel()

	if got := capabilities(Interpret(Invocation{Executable: "tox"})); !slices.Equal(got, []plan.Capability{plan.CapabilityTestRun}) {
		t.Fatalf("Interpret(tox) = %v, want test.run", got)
	}
	if got := capabilities(Interpret(Invocation{Executable: "tox", Args: []string{"run"}})); !slices.Equal(got, []plan.Capability{plan.CapabilityTestRun}) {
		t.Fatalf("Interpret(tox run) = %v, want test.run", got)
	}
	if got := capabilities(Interpret(Invocation{Executable: "tox", Args: []string{"run", "-e", "typing"}})); len(got) != 0 {
		t.Fatalf("Interpret(tox run -e typing) = %v, want no matches", got)
	}
	if got := capabilities(Interpret(Invocation{Executable: "nox"})); !slices.Equal(got, []plan.Capability{plan.CapabilityTestRun}) {
		t.Fatalf("Interpret(nox) = %v, want test.run", got)
	}
	if got := capabilities(Interpret(Invocation{Executable: "nox", Args: []string{"-s", "lint"}})); len(got) != 0 {
		t.Fatalf("Interpret(nox -s lint) = %v, want no matches", got)
	}
}

func TestParseScriptCapturesUvRunDirectory(t *testing.T) {
	t.Parallel()

	got := ParseScript(`uv run --directory packages/api pytest && uv run -C packages/web python -m pytest && uv run --directory=packages/cli --locked pytest && uv --directory packages/api run pytest && uv -C packages/web tool run ruff && uv run --env-file .env pytest && uv run --with-requirements extras.txt pytest`)
	want := []Invocation{
		{Executable: "pytest", Directory: "packages/api"},
		{Executable: "pytest", Directory: "packages/web"},
		{Executable: "pytest", Directory: "packages/cli"},
		{Executable: "pytest", Directory: "packages/api"},
		{Executable: "ruff", Directory: "packages/web"},
		{Executable: "pytest"},
		{Executable: "pytest"},
	}
	if !slices.EqualFunc(got, want, invocationsEqual) {
		t.Fatalf("ParseScript() = %#v, want %#v", got, want)
	}
}

func TestInterpretMatchesPythonInvocations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		invocation Invocation
		capability plan.Capability
	}{
		{invocation: Invocation{Executable: "pip", Args: []string{"install", "-e", "."}}, capability: plan.CapabilityDependenciesInstall},
		{invocation: Invocation{Executable: "uv", Args: []string{"sync"}}, capability: plan.CapabilityDependenciesInstall},
		{invocation: Invocation{Executable: "poetry", Args: []string{"install"}}, capability: plan.CapabilityDependenciesInstall},
		{invocation: Invocation{Executable: "pytest"}, capability: plan.CapabilityTestRun},
		{invocation: Invocation{Executable: "python", Args: []string{"manage.py", "test"}}, capability: plan.CapabilityTestRun},
		{invocation: Invocation{Executable: "python", Args: []string{"manage.py", "runserver"}}, capability: plan.CapabilityApplicationRun},
		{invocation: Invocation{Executable: "flask", Args: []string{"run"}}, capability: plan.CapabilityApplicationRun},
		{invocation: Invocation{Executable: "ruff", Args: []string{"check"}}, capability: plan.CapabilityCodeLint},
		{invocation: Invocation{Executable: "mypy"}, capability: plan.CapabilityCodeTypecheck},
	}
	for _, tt := range tests {
		matches := Interpret(tt.invocation)
		if len(matches) != 1 || matches[0].Capability != tt.capability {
			t.Fatalf("Interpret(%+v) = %+v, want %s", tt.invocation, matches, tt.capability)
		}
	}
}


func TestIsRemotePipInstall(t *testing.T) {
	t.Parallel()

	if !IsRemotePipInstall(Invocation{Executable: "pip", Args: []string{"install", "ruff", "pytest"}}) {
		t.Fatal("IsRemotePipInstall(pip install ruff pytest) = false, want true")
	}
	if IsRemotePipInstall(Invocation{Executable: "pip", Args: []string{"install", "-r", "requirements.txt"}}) {
		t.Fatal("IsRemotePipInstall(requirements file) = true, want false")
	}
	if IsRemotePipInstall(Invocation{Executable: "pip", Args: []string{"install", "-e", "."}}) {
		t.Fatal("IsRemotePipInstall(editable project) = true, want false")
	}
	if IsRemotePipInstall(Invocation{Executable: "pip", Args: []string{"install", "../shared"}}) {
		t.Fatal("IsRemotePipInstall(relative parent path) = true, want false")
	}
	if IsRemotePipInstall(Invocation{Executable: "pip", Args: []string{"install", "packages/widget"}}) {
		t.Fatal("IsRemotePipInstall(relative package path) = true, want false")
	}
	if !IsRemotePipInstall(Invocation{Executable: "pip", Args: []string{"install", "-c", "constraints.txt", "tox"}}) {
		t.Fatal("IsRemotePipInstall(constraint plus named package) = false, want true")
	}
	if !IsRemotePipInstall(Invocation{Executable: "uv", Args: []string{"--directory", ".", "pip", "install", "ruff"}}) {
		t.Fatal("IsRemotePipInstall(uv --directory . pip install ruff) = false, want true")
	}
	if !IsRemotePipInstall(Invocation{Executable: "pip", Args: []string{"--index-url", "https://pypi.org/simple", "install", "tox"}}) {
		t.Fatal("IsRemotePipInstall(pip --index-url … install tox) = false, want true")
	}
}


func TestIsRemoteGemInstall(t *testing.T) {
	t.Parallel()

	if !IsRemoteGemInstall(Invocation{Executable: "gem", Args: []string{"install", "test-unit", "coveralls"}}) {
		t.Fatal("IsRemoteGemInstall(gem install test-unit coveralls) = false, want true")
	}
	if IsRemoteGemInstall(Invocation{Executable: "gem", Args: []string{"install", "./pkg/widget-1.0.0.gem"}}) {
		t.Fatal("IsRemoteGemInstall(local gem archive) = true, want false")
	}
}

func TestIsSystemPackagePlumbing(t *testing.T) {
	t.Parallel()

	for _, invocation := range []Invocation{
		{Executable: "apt-get", Args: []string{"update"}},
		{Executable: "sudo", Args: []string{"apt-get", "install", "-y", "libvips"}},
	} {
		if !IsSystemPackagePlumbing(invocation) {
			t.Fatalf("IsSystemPackagePlumbing(%+v) = false, want true", invocation)
		}
	}
	if IsSystemPackagePlumbing(Invocation{Executable: "sudo", Args: []string{"bin/rails", "test"}}) {
		t.Fatal("IsSystemPackagePlumbing(sudo bin/rails test) = true, want false")
	}
}

func TestInterpretScriptCollectsEachMatchedCapabilityOnce(t *testing.T) {
	t.Parallel()

	matches := InterpretScript("xo && c8 ava && tsc --noEmit --types node source/index.d.ts")
	got := capabilities(matches)
	want := []plan.Capability{plan.CapabilityCodeLint, plan.CapabilityTestRun, plan.CapabilityCodeTypecheck}
	if !slices.Equal(got, want) {
		t.Fatalf("InterpretScript() = %v, want %v", got, want)
	}
}

func capabilities(matches []Match) []plan.Capability {
	out := make([]plan.Capability, 0, len(matches))
	for _, match := range matches {
		out = append(out, match.Capability)
	}
	return out
}

func TestRedactAssignmentValuesStripsLiteralValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw  string
		want string
	}{
		{raw: "API_TOKEN=hunter2 npm test", want: "API_TOKEN=$API_TOKEN npm test"},
		{raw: `FOO='bar baz' BAR="qux" npm test`, want: "FOO=$FOO BAR=$BAR npm test"},
		{raw: "npm test", want: "npm test"},
		{raw: "platform=${{ matrix.platform }}", want: "platform=$platform"},
		{raw: "platform=${{ matrix.platform }} docker build .", want: "platform=$platform docker build ."},
	}
	for _, tt := range tests {
		if got := RedactAssignmentValues(tt.raw); got != tt.want {
			t.Fatalf("RedactAssignmentValues(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestStripDirectoryFlagsStopsAtDoubleDash(t *testing.T) {
	t.Parallel()

	dir, got := StripDirectoryFlags(Invocation{Executable: "npm", Args: []string{"test", "--", "--prefix", "fixtures"}})
	if dir != "" {
		t.Fatalf("dir = %q, want empty", dir)
	}
	want := Invocation{Executable: "npm", Args: []string{"test", "--", "--prefix", "fixtures"}}
	if !invocationsEqual(got, want) {
		t.Fatalf("canonical = %+v, want %+v", got, want)
	}
}

func TestStripDirectoryFlagsUsesCargoCNotManifestPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		args []string
		want string
	}{
		{args: []string{"test", "--manifest-path", "Cargo.toml"}, want: ""},
		{args: []string{"test", "--manifest-path", "./Cargo.toml"}, want: ""},
		{args: []string{"test", "--manifest-path=./Cargo.toml"}, want: ""},
		{args: []string{"test", "--manifest-path", "crates/tool/Cargo.toml"}, want: ""},
		{args: []string{"-C", "crates/tool", "test", "--manifest-path", "Cargo.toml"}, want: "crates/tool"},
		{args: []string{"-C", "crates", "test", "--manifest-path", "tool/Cargo.toml"}, want: "crates"},
		{args: []string{"-C", "crates/tool", "test", "--manifest-path", "../cli/Cargo.toml"}, want: "crates/tool"},
	}
	for _, tt := range tests {
		dir, got := StripDirectoryFlags(Invocation{Executable: "cargo", Args: tt.args})
		if dir != tt.want {
			t.Fatalf("dir(%v) = %q, want %q", tt.args, dir, tt.want)
		}
		if got.Executable != "cargo" || !slices.Equal(got.Args, []string{"test"}) {
			t.Fatalf("canonical(%v) = %+v, want cargo test", tt.args, got)
		}
	}
}

func TestRewriteDirectoryFlagsStripsCargoPaths(t *testing.T) {
	t.Parallel()

	got := RewriteDirectoryFlags(
		"cargo test --manifest-path crates/tool/Cargo.toml --locked",
		Invocation{Executable: "cargo", Args: []string{"test", "--manifest-path", "crates/tool/Cargo.toml", "--locked"}},
	)
	if got != "cargo test --manifest-path crates/tool/Cargo.toml --locked" {
		t.Fatalf("RewriteDirectoryFlags(manifest-path) = %q, want the original invocation", got)
	}
	got = RewriteDirectoryFlags(
		"cargo +nightly -C crates/tool test",
		Invocation{Executable: "cargo", Args: []string{"-C", "crates/tool", "test"}},
	)
	if got != "cargo +nightly test" {
		t.Fatalf("RewriteDirectoryFlags(-C) = %q, want cargo +nightly test", got)
	}
	got = RewriteDirectoryFlags(
		"cargo -C crates/tool test --manifest-path Cargo.toml",
		Invocation{Executable: "cargo", Args: []string{"-C", "crates/tool", "test", "--manifest-path", "Cargo.toml"}},
	)
	if got != "cargo test --manifest-path Cargo.toml" {
		t.Fatalf("RewriteDirectoryFlags(-C and manifest-path) = %q, want cargo test with the manifest path kept", got)
	}
	got = RewriteDirectoryFlags(
		`cargo test --manifest-path crates/tool/Cargo.toml --features "foo bar"`,
		Invocation{Executable: "cargo", Args: []string{"test", "--manifest-path", "crates/tool/Cargo.toml", "--features", "foo bar"}},
	)
	if got != `cargo test --manifest-path crates/tool/Cargo.toml --features "foo bar"` {
		t.Fatalf("RewriteDirectoryFlags(quoted features) = %q, want the original invocation", got)
	}
	got = RewriteDirectoryFlags(
		`cargo -C crates/tool test --features 'foo bar'`,
		Invocation{Executable: "cargo", Args: []string{"-C", "crates/tool", "test", "--features", "foo bar"}},
	)
	if got != `cargo test --features 'foo bar'` {
		t.Fatalf("RewriteDirectoryFlags(quoted -C) = %q, want quotes preserved", got)
	}
	got = RewriteDirectoryFlags(
		"rustup run nightly cargo test --manifest-path crates/tool/Cargo.toml",
		Invocation{Executable: "cargo", Args: []string{"test", "--manifest-path", "crates/tool/Cargo.toml"}},
	)
	if got != "rustup run nightly cargo test --manifest-path crates/tool/Cargo.toml" {
		t.Fatalf("RewriteDirectoryFlags(rustup) = %q, want the original rustup invocation", got)
	}
	got = RewriteDirectoryFlags(
		`cargo test --manifest-path "$SEMAPHORE_GIT_DIR/crates/tool/Cargo.toml"`,
		Invocation{Executable: "cargo", Args: []string{"test", "--manifest-path", "$SEMAPHORE_GIT_DIR/crates/tool/Cargo.toml"}},
	)
	if got != `cargo test --manifest-path "$SEMAPHORE_GIT_DIR/crates/tool/Cargo.toml"` {
		t.Fatalf("RewriteDirectoryFlags(dynamic) = %q, want the original run", got)
	}
	got = RewriteDirectoryFlags(
		"yarn --cwd ./packages/app test",
		Invocation{Executable: "yarn", Args: []string{"--cwd", "./packages/app", "test"}},
	)
	if got != "yarn --cwd ./packages/app test" {
		t.Fatalf("RewriteDirectoryFlags(yarn) = %q, want the original run", got)
	}
	got = RewriteDirectoryFlags(
		"cargo -C crate test > result.log",
		Invocation{Executable: "cargo", Args: []string{"-C", "crate", "test"}},
	)
	if got != "cargo -C crate test > result.log" {
		t.Fatalf("RewriteDirectoryFlags(redirect) = %q, want the original -C form", got)
	}
	got = RewriteDirectoryFlags(
		"cargo -C crate test | tee result.log",
		Invocation{Executable: "cargo", Args: []string{"-C", "crate", "test"}},
	)
	if got != "cargo -C crate test | tee result.log" {
		t.Fatalf("RewriteDirectoryFlags(pipe) = %q, want the original -C form", got)
	}
}

func TestWorkingDirectoryKeepsShellCwdForCargoRedirects(t *testing.T) {
	t.Parallel()

	inv := Invocation{Executable: "cargo", Args: []string{"-C", "crate", "test"}}
	if got := WorkingDirectory("cargo -C crate test", inv); got != "crate" {
		t.Fatalf("WorkingDirectory(simple) = %q, want crate", got)
	}
	if got := WorkingDirectory("cargo -C crate test > result.log", inv); got != "" {
		t.Fatalf("WorkingDirectory(redirect) = %q, want empty so result.log stays at the shell cwd", got)
	}
	if got := WorkingDirectory("cargo -C crate test | tee result.log", inv); got != "" {
		t.Fatalf("WorkingDirectory(pipe) = %q, want empty", got)
	}

	phpunit := Invocation{Executable: "phpunit"}
	if got := WorkingDirectory("composer -d tools exec phpunit", phpunit); got != "tools" {
		t.Fatalf("WorkingDirectory(composer exec) = %q, want tools after unwrap", got)
	}
}

func TestStripDirectoryFlagsLeavesDynamicCargoPaths(t *testing.T) {
	t.Parallel()

	dir, got := StripDirectoryFlags(Invocation{
		Executable: "cargo",
		Args:       []string{"test", "--manifest-path", "$SEMAPHORE_GIT_DIR/crates/tool/Cargo.toml"},
	})
	if dir != "" {
		t.Fatalf("dir = %q, want empty for a variable-valued manifest path", dir)
	}
	if !slices.Equal(got.Args, []string{"test", "--manifest-path", "$SEMAPHORE_GIT_DIR/crates/tool/Cargo.toml"}) {
		t.Fatalf("args = %v, want the original invocation", got.Args)
	}

	dir, got = StripDirectoryFlags(Invocation{
		Executable: "cargo",
		Args:       []string{"-C", "${{ matrix.crate }}", "test"},
	})
	if dir != "" {
		t.Fatalf("dir = %q, want empty for an expression-valued -C", dir)
	}
	if !slices.Equal(got.Args, []string{"-C", "${{ matrix.crate }}", "test"}) {
		t.Fatalf("args = %v, want the original invocation", got.Args)
	}
}

func TestStripDirectoryFlagsRemovesYarnCwd(t *testing.T) {
	t.Parallel()

	dir, got := StripDirectoryFlags(Invocation{Executable: "yarn", Args: []string{"--cwd", "./packages/app", "test", "--watch=false"}})
	if dir != "./packages/app" {
		t.Fatalf("dir = %q, want ./packages/app", dir)
	}
	want := Invocation{Executable: "yarn", Args: []string{"test", "--watch=false"}, Directory: "./packages/app"}
	if !invocationsEqual(got, want) {
		t.Fatalf("canonical = %+v, want %+v", got, want)
	}
}

func TestStripDirectoryFlagsPreservesUvRunDirectory(t *testing.T) {
	t.Parallel()

	dir, got := StripDirectoryFlags(Invocation{Executable: "pytest", Directory: "packages/api"})
	if dir != "packages/api" {
		t.Fatalf("dir = %q, want packages/api", dir)
	}
	want := Invocation{Executable: "pytest", Directory: "packages/api"}
	if !invocationsEqual(got, want) {
		t.Fatalf("canonical = %+v, want %+v", got, want)
	}
}

func TestIsComposeUp(t *testing.T) {
	t.Parallel()

	if !IsComposeUp(Invocation{Executable: "docker", Args: []string{"compose", "up", "-d", "postgres"}}) {
		t.Fatal("IsComposeUp(docker compose up -d) = false, want true")
	}
	if !IsComposeUp(Invocation{Executable: "docker-compose", Args: []string{"up", "-d"}}) {
		t.Fatal("IsComposeUp(docker-compose up -d) = false, want true")
	}
	if IsComposeUp(Invocation{Executable: "docker", Args: []string{"compose", "ps"}}) {
		t.Fatal("IsComposeUp(docker compose ps) = true, want false")
	}
	if IsComposeUp(Invocation{Executable: "docker", Args: []string{"run", "postgres"}}) {
		t.Fatal("IsComposeUp(docker run) = true, want false")
	}
	if !IsComposeUp(Invocation{Executable: "docker", Args: []string{"compose", "--profile", "tests", "up", "-d"}}) {
		t.Fatal("IsComposeUp(docker compose --profile tests up -d) = false, want true")
	}
	if !IsComposeUp(Invocation{Executable: "docker", Args: []string{"compose", "-f", "ci.yml", "up", "-d"}}) {
		t.Fatal("IsComposeUp(docker compose -f ci.yml up -d) = false, want true")
	}
	if !IsComposeUp(Invocation{Executable: "docker", Args: []string{"compose", "--file=ci.yml", "up"}}) {
		t.Fatal("IsComposeUp(docker compose --file=ci.yml up) = false, want true")
	}
}

func TestInterpretMatchesGoToolchainInvocations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		inv  Invocation
		want []plan.Capability
	}{
		{inv: Invocation{Executable: "go", Args: []string{"test", "./..."}}, want: []plan.Capability{plan.CapabilityTestRun}},
		{inv: Invocation{Executable: "go", Args: []string{"test", "-race", "./..."}}, want: []plan.Capability{plan.CapabilityTestRun}},
		{inv: Invocation{Executable: "go", Args: []string{"build", "./..."}}, want: []plan.Capability{plan.CapabilityArtifactBuild}},
		{inv: Invocation{Executable: "go", Args: []string{"vet", "./..."}}, want: []plan.Capability{plan.CapabilityCodeLint}},
		{inv: Invocation{Executable: "go", Args: []string{"mod", "download"}}, want: []plan.Capability{plan.CapabilityDependenciesInstall}},
		{inv: Invocation{Executable: "golangci-lint", Args: []string{"run", "--verbose"}}, want: []plan.Capability{plan.CapabilityCodeLint}},
		{inv: Invocation{Executable: "gofmt", Args: []string{"-l", "."}}, want: []plan.Capability{plan.CapabilityCodeFormat}},
		{inv: Invocation{Executable: "go", Args: []string{"get", "-v", "./..."}}, want: []plan.Capability{plan.CapabilityDependenciesInstall}},
	}
	for _, tt := range tests {
		got := capabilities(Interpret(tt.inv))
		if !slices.Equal(got, tt.want) {
			t.Fatalf("Interpret(%s %v) = %v, want %v", tt.inv.Executable, tt.inv.Args, got, tt.want)
		}
	}
}

func TestInterpretMatchesMixInvocations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		inv  Invocation
		want []plan.Capability
	}{
		{inv: Invocation{Executable: "mix", Args: []string{"deps.get"}}, want: []plan.Capability{plan.CapabilityDependenciesInstall}},
		{inv: Invocation{Executable: "mix", Args: []string{"compile", "--warnings-as-errors"}}, want: []plan.Capability{plan.CapabilityArtifactBuild}},
		{inv: Invocation{Executable: "mix", Args: []string{"test"}}, want: []plan.Capability{plan.CapabilityTestRun}},
		{inv: Invocation{Executable: "mix", Args: []string{"credo", "--strict"}}, want: []plan.Capability{plan.CapabilityCodeLint}},
		{inv: Invocation{Executable: "mix", Args: []string{"dialyzer"}}, want: []plan.Capability{plan.CapabilityCodeTypecheck}},
		{inv: Invocation{Executable: "mix", Args: []string{"format", "--check-formatted"}}, want: []plan.Capability{plan.CapabilityCodeFormat}},
		{inv: Invocation{Executable: "mix", Args: []string{"phx.server"}}, want: []plan.Capability{plan.CapabilityApplicationRun}},
	}
	for _, tt := range tests {
		got := capabilities(Interpret(tt.inv))
		if !slices.Equal(got, tt.want) {
			t.Fatalf("Interpret(%s %v) = %v, want %v", tt.inv.Executable, tt.inv.Args, got, tt.want)
		}
	}
}

func TestInterpretMatchesCargoInvocations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		inv  Invocation
		want []plan.Capability
	}{
		{inv: Invocation{Executable: "cargo", Args: []string{"test"}}, want: []plan.Capability{plan.CapabilityTestRun}},
		{inv: Invocation{Executable: "cargo", Args: []string{"+nightly", "test", "--locked"}}, want: []plan.Capability{plan.CapabilityTestRun}},
		{inv: Invocation{Executable: "cargo", Args: []string{"--locked", "test"}}, want: []plan.Capability{plan.CapabilityTestRun}},
		{inv: Invocation{Executable: "cargo", Args: []string{"--color", "always", "test"}}, want: []plan.Capability{plan.CapabilityTestRun}},
		{inv: Invocation{Executable: "cargo", Args: []string{"--offline", "--locked", "build", "--release"}}, want: []plan.Capability{plan.CapabilityArtifactBuild}},
		{inv: Invocation{Executable: "cargo", Args: []string{"build", "--release"}}, want: []plan.Capability{plan.CapabilityArtifactBuild}},
		{inv: Invocation{Executable: "cargo", Args: []string{"fetch"}}, want: []plan.Capability{plan.CapabilityDependenciesInstall}},
		{inv: Invocation{Executable: "cargo", Args: []string{"clippy", "--", "-D", "warnings"}}, want: []plan.Capability{plan.CapabilityCodeLint}},
		{inv: Invocation{Executable: "cargo", Args: []string{"fmt", "--check"}}, want: []plan.Capability{plan.CapabilityCodeFormat}},
		{inv: Invocation{Executable: "cargo", Args: []string{"check"}}, want: []plan.Capability{plan.CapabilityCodeTypecheck}},
		{inv: Invocation{Executable: "cargo", Args: []string{"-Zunstable-options", "check"}}, want: []plan.Capability{plan.CapabilityCodeTypecheck}},
		{inv: Invocation{Executable: "cargo", Args: []string{"-Z", "unstable-options", "check"}}, want: []plan.Capability{plan.CapabilityCodeTypecheck}},
		{inv: Invocation{Executable: "cargo", Args: []string{"run"}}, want: []plan.Capability{plan.CapabilityApplicationRun}},
		{inv: Invocation{Executable: "cargo", Args: []string{"nextest", "run"}}, want: []plan.Capability{plan.CapabilityTestRun}},
	}
	for _, tt := range tests {
		got := capabilities(Interpret(tt.inv))
		if !slices.Equal(got, tt.want) {
			t.Fatalf("Interpret(%s %v) = %v, want %v", tt.inv.Executable, tt.inv.Args, got, tt.want)
		}
	}
}

func TestIsRemoteGoInstall(t *testing.T) {
	t.Parallel()

	if !IsRemoteGoInstall(Invocation{Executable: "go", Args: []string{"install", "github.com/kyoh86/richgo@latest"}}) {
		t.Fatal("IsRemoteGoInstall(go install github.com/...) = false, want true")
	}
	if IsRemoteGoInstall(Invocation{Executable: "go", Args: []string{"install", "./cmd/suss"}}) {
		t.Fatal("IsRemoteGoInstall(go install ./cmd/suss) = true, want false")
	}
	if IsRemoteGoInstall(Invocation{Executable: "go", Args: []string{"test", "./..."}}) {
		t.Fatal("IsRemoteGoInstall(go test) = true, want false")
	}
}

func TestIsGoPlumbing(t *testing.T) {
	t.Parallel()

	if !IsGoPlumbing(Invocation{Executable: "go", Args: []string{"env"}}) {
		t.Fatal("IsGoPlumbing(go env) = false, want true")
	}
	if !IsGoPlumbing(Invocation{Executable: "go", Args: []string{"version"}}) {
		t.Fatal("IsGoPlumbing(go version) = false, want true")
	}
	if IsGoPlumbing(Invocation{Executable: "go", Args: []string{"test", "./..."}}) {
		t.Fatal("IsGoPlumbing(go test) = true, want false")
	}
}

func TestIsRemoteCargoInstall(t *testing.T) {
	t.Parallel()

	if !IsRemoteCargoInstall(Invocation{Executable: "cargo", Args: []string{"install", "cargo-nextest"}}) {
		t.Fatal("IsRemoteCargoInstall(cargo install cargo-nextest) = false, want true")
	}
	if !IsRemoteCargoInstall(Invocation{Executable: "cargo", Args: []string{"install", "--locked", "cargo-deny"}}) {
		t.Fatal("IsRemoteCargoInstall(cargo install --locked cargo-deny) = false, want true")
	}
	if !IsRemoteCargoInstall(Invocation{Executable: "cargo", Args: []string{"--locked", "install", "cargo-nextest"}}) {
		t.Fatal("IsRemoteCargoInstall(cargo --locked install cargo-nextest) = false, want true")
	}
	if IsRemoteCargoInstall(Invocation{Executable: "cargo", Args: []string{"install", "--path", "."}}) {
		t.Fatal("IsRemoteCargoInstall(cargo install --path .) = true, want false")
	}
	if IsRemoteCargoInstall(Invocation{Executable: "cargo", Args: []string{"install"}}) {
		t.Fatal("IsRemoteCargoInstall(cargo install) = true, want false")
	}
	if IsRemoteCargoInstall(Invocation{Executable: "cargo", Args: []string{"test"}}) {
		t.Fatal("IsRemoteCargoInstall(cargo test) = true, want false")
	}
}

func TestIsRustPlumbing(t *testing.T) {
	t.Parallel()

	if !IsRustPlumbing(Invocation{Executable: "rustup", Args: []string{"update"}}) {
		t.Fatal("IsRustPlumbing(rustup update) = false, want true")
	}
	if !IsRustPlumbing(Invocation{Executable: "cargo", Args: []string{"--version"}}) {
		t.Fatal("IsRustPlumbing(cargo --version) = false, want true")
	}
	if !IsRustPlumbing(Invocation{Executable: "cargo", Args: []string{"version"}}) {
		t.Fatal("IsRustPlumbing(cargo version) = false, want true")
	}
	if IsRustPlumbing(Invocation{Executable: "cargo", Args: []string{"test"}}) {
		t.Fatal("IsRustPlumbing(cargo test) = true, want false")
	}
	if !IsRustPlumbing(Invocation{Executable: "cargo", Args: []string{"-V"}}) {
		t.Fatal("IsRustPlumbing(cargo -V) = false, want true")
	}
	if IsRustPlumbing(Invocation{Executable: "cargo", Args: []string{"-v"}}) {
		t.Fatal("IsRustPlumbing(cargo -v) = true, want false; -v is verbose")
	}
	if IsRustPlumbing(Invocation{Executable: "cargo", Args: []string{"test", "-v"}}) {
		t.Fatal("IsRustPlumbing(cargo test -v) = true, want false")
	}
	if !IsRustPlumbing(Invocation{Executable: "rustc", Args: []string{"-V"}}) {
		t.Fatal("IsRustPlumbing(rustc -V) = false, want true")
	}
	if !IsRustPlumbing(Invocation{Executable: "rustc", Args: []string{"-Vv"}}) {
		t.Fatal("IsRustPlumbing(rustc -Vv) = false, want true")
	}
	if !IsRustPlumbing(Invocation{Executable: "rustc", Args: []string{"-vV"}}) {
		t.Fatal("IsRustPlumbing(rustc -vV) = false, want true")
	}
	if IsRustPlumbing(Invocation{Executable: "rustc", Args: []string{"-v"}}) {
		t.Fatal("IsRustPlumbing(rustc -v) = true, want false; -v is verbose")
	}
	if IsRustPlumbing(Invocation{Executable: "rustup", Args: []string{"run", "nightly", "cargo", "test"}}) {
		t.Fatal("IsRustPlumbing(rustup run nightly cargo test) = true, want false")
	}
}

func TestParseScriptUnwrapsRustupRun(t *testing.T) {
	t.Parallel()

	got := ParseScript("rustup run nightly cargo test --locked")
	if len(got) != 1 || got[0].Executable != "cargo" || !slices.Equal(got[0].Args, []string{"test", "--locked"}) {
		t.Fatalf("ParseScript(rustup run) = %+v, want cargo test --locked", got)
	}
	matches := Interpret(got[0])
	if !slices.Equal(capabilities(matches), []plan.Capability{plan.CapabilityTestRun}) {
		t.Fatalf("Interpret(unwrapped rustup run) = %v, want test.run", capabilities(matches))
	}
}

func TestIsToolPlumbingCoversVersionProbes(t *testing.T) {
	t.Parallel()

	if !IsToolPlumbing(Invocation{Executable: "docker", Args: []string{"version"}}) {
		t.Fatal("IsToolPlumbing(docker version) = false, want true")
	}
	if !IsToolPlumbing(Invocation{Executable: "docker", Args: []string{"info"}}) {
		t.Fatal("IsToolPlumbing(docker info) = false, want true")
	}
	if !IsToolPlumbing(Invocation{Executable: "docker", Args: []string{"compose", "version"}}) {
		t.Fatal("IsToolPlumbing(docker compose version) = false, want true")
	}
	if IsToolPlumbing(Invocation{Executable: "docker", Args: []string{"compose", "up", "-d"}}) {
		t.Fatal("IsToolPlumbing(docker compose up) = true, want false")
	}
	if IsToolPlumbing(Invocation{Executable: "make", Args: []string{"version"}}) {
		t.Fatal("IsToolPlumbing(make version) = true, want false")
	}
	if !IsToolPlumbing(Invocation{Executable: "npm", Args: []string{"--version"}}) {
		t.Fatal("IsToolPlumbing(npm --version) = false, want true")
	}
	if IsToolPlumbing(Invocation{Executable: "npm", Args: []string{"version", "--no-git-tag-version", "1.2.3"}}) {
		t.Fatal("IsToolPlumbing(npm version ...) = true, want false; npm version bumps the package")
	}
	if IsToolPlumbing(Invocation{Executable: "composer", Args: []string{"-v", "install"}}) {
		t.Fatal("IsToolPlumbing(composer -v install) = true, want false; -v is verbosity")
	}
	if !IsToolPlumbing(Invocation{Executable: "composer", Args: []string{"-V"}}) {
		t.Fatal("IsToolPlumbing(composer -V) = false, want true")
	}
	if !IsToolPlumbing(Invocation{Executable: "composer", Args: []string{"--version"}}) {
		t.Fatal("IsToolPlumbing(composer --version) = false, want true")
	}
	if !IsToolPlumbing(Invocation{Executable: "php", Args: []string{"-v"}}) {
		t.Fatal("IsToolPlumbing(php -v) = false, want true")
	}
	if !IsToolPlumbing(Invocation{Executable: "php", Args: []string{"-i"}}) {
		t.Fatal("IsToolPlumbing(php -i) = false, want true")
	}
	if !IsToolPlumbing(Invocation{Executable: "php", Args: []string{"--ini"}}) {
		t.Fatal("IsToolPlumbing(php --ini) = false, want true")
	}
	if !IsToolPlumbing(Invocation{Executable: "php", Args: []string{"-m"}}) {
		t.Fatal("IsToolPlumbing(php -m) = false, want true")
	}
	if IsToolPlumbing(Invocation{Executable: "php", Args: []string{"artisan", "test"}}) {
		t.Fatal("IsToolPlumbing(php artisan test) = true, want false")
	}
	if IsToolPlumbing(Invocation{Executable: "pip", Args: []string{"-v", "install", "-r", "requirements.txt"}}) {
		t.Fatal("IsToolPlumbing(pip -v install) = true, want false; -v is verbose")
	}
	if IsToolPlumbing(Invocation{Executable: "uv", Args: []string{"-v", "sync"}}) {
		t.Fatal("IsToolPlumbing(uv -v sync) = true, want false; -v is verbose")
	}
	if IsToolPlumbing(Invocation{Executable: "poetry", Args: []string{"-v", "install"}}) {
		t.Fatal("IsToolPlumbing(poetry -v install) = true, want false; -v is verbose")
	}
	if !IsToolPlumbing(Invocation{Executable: "pip", Args: []string{"--version"}}) {
		t.Fatal("IsToolPlumbing(pip --version) = false, want true")
	}
}

func TestParseScriptKeepsGHAExpressionsAtomic(t *testing.T) {
	t.Parallel()

	got := ParseScript("platform=${{ matrix.platform }}")
	if len(got) != 0 {
		t.Fatalf("ParseScript(assignment only) = %#v, want no invocation", got)
	}

	got = ParseScript("platform=${{ matrix.platform }} docker build .")
	if len(got) != 1 || got[0].Executable != "docker" {
		t.Fatalf("ParseScript() = %#v, want docker build", got)
	}

	got = ParseScript("echo ${{ steps.build.outputs.digest }}")
	if len(got) != 1 || got[0].Executable != "echo" {
		t.Fatalf("ParseScript(echo expr) = %#v, want echo", got)
	}

	got = ParseScript("broken=${{ matrix.platform")
	if len(got) != 0 {
		t.Fatalf("ParseScript(unclosed) = %#v, want no fabricated invocation", got)
	}
}

func TestInterpretMatchesShortYarnFrozenInstall(t *testing.T) {
	t.Parallel()

	matches := Interpret(Invocation{Executable: "yarn", Args: []string{"--frozen-lockfile"}})
	got := capabilities(matches)
	want := []plan.Capability{plan.CapabilityDependenciesInstall}
	if !slices.Equal(got, want) {
		t.Fatalf("Interpret(yarn --frozen-lockfile) = %v, want %v", got, want)
	}
}

func TestClassifyManagerTreatsYarnScriptAndInstall(t *testing.T) {
	t.Parallel()

	script, ok := ClassifyManager(Invocation{Executable: "yarn", Args: []string{"run", "test:app", "--watch=false"}})
	if !ok || script.Manager != "yarn" || script.Script != "test:app" || !slices.Equal(script.Args, []string{"--watch=false"}) {
		t.Fatalf("ClassifyManager(yarn run test:app) = %+v, ok=%v", script, ok)
	}

	bare, ok := ClassifyManager(Invocation{Executable: "yarn", Args: []string{"--frozen-lockfile"}})
	if !ok || !bare.Install || bare.Script != "" {
		t.Fatalf("ClassifyManager(yarn --frozen-lockfile) = %+v, ok=%v", bare, ok)
	}

	npmTest, ok := ClassifyManager(Invocation{Executable: "npm", Args: []string{"test", "--coverage"}})
	if !ok || npmTest.Script != "test" || !slices.Equal(npmTest.Args, []string{"--coverage"}) {
		t.Fatalf("ClassifyManager(npm test) = %+v, ok=%v", npmTest, ok)
	}
}

func TestClassifyManagerReadsPnpmFilterAndSkipsGlobalInstall(t *testing.T) {
	t.Parallel()

	filtered, ok := ClassifyManager(Invocation{Executable: "pnpm", Args: []string{"--filter", "mermaid", "run", "docs:build:vitepress"}})
	if !ok || filtered.Script != "docs:build:vitepress" || filtered.Install {
		t.Fatalf("ClassifyManager(pnpm --filter … run) = %+v, ok=%v", filtered, ok)
	}

	runFilter, ok := ClassifyManager(Invocation{Executable: "pnpm", Args: []string{"run", "--filter", "mermaid", "types:build-config"}})
	if !ok || runFilter.Script != "types:build-config" {
		t.Fatalf("ClassifyManager(pnpm run --filter) = %+v, ok=%v", runFilter, ok)
	}

	global, ok := ClassifyManager(Invocation{Executable: "npm", Args: []string{"i", "json@11.0.0", "--global"}})
	if !ok || global.Install || global.Script != "" {
		t.Fatalf("ClassifyManager(npm i --global) = %+v, ok=%v", global, ok)
	}
	if !IsGlobalInstall(Invocation{Executable: "npm", Args: []string{"install", "-g", "npm@11"}}) {
		t.Fatal("IsGlobalInstall(npm install -g) = false, want true")
	}
}

func TestClassifyManagerTreatsPythonInstalls(t *testing.T) {
	t.Parallel()

	uvSync, ok := ClassifyManager(Invocation{Executable: "uv", Args: []string{"sync"}})
	if !ok || uvSync.Manager != "uv" || !uvSync.Install {
		t.Fatalf("ClassifyManager(uv sync) = %+v, ok=%v", uvSync, ok)
	}
	poetry, ok := ClassifyManager(Invocation{Executable: "poetry", Args: []string{"install"}})
	if !ok || poetry.Manager != "poetry" || !poetry.Install {
		t.Fatalf("ClassifyManager(poetry install) = %+v, ok=%v", poetry, ok)
	}
	pip, ok := ClassifyManager(Invocation{Executable: "pip", Args: []string{"install", "-r", "requirements.txt"}})
	if !ok || pip.Manager != "pip" || !pip.Install {
		t.Fatalf("ClassifyManager(pip install) = %+v, ok=%v", pip, ok)
	}
	pdm, ok := ClassifyManager(Invocation{Executable: "pdm", Args: []string{"sync"}})
	if !ok || pdm.Manager != "pdm" || !pdm.Install {
		t.Fatalf("ClassifyManager(pdm sync) = %+v, ok=%v", pdm, ok)
	}
	run, ok := ClassifyManager(Invocation{Executable: "uv", Args: []string{"run", "pytest"}})
	if !ok || run.Install {
		t.Fatalf("ClassifyManager(uv run pytest) = %+v, ok=%v, did not want install", run, ok)
	}
}

func statementRaws(statements []Statement) []string {
	if len(statements) == 0 {
		return nil
	}
	out := make([]string, 0, len(statements))
	for _, stmt := range statements {
		out = append(out, stmt.Raw)
	}
	return out
}

func invocationsEqual(a, b Invocation) bool {
	return a.Executable == b.Executable && slices.Equal(a.Args, b.Args) && a.Directory == b.Directory
}
