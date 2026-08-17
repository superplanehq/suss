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

func TestStripDirectoryFlagsRemovesYarnCwd(t *testing.T) {
	t.Parallel()

	dir, got := StripDirectoryFlags(Invocation{Executable: "yarn", Args: []string{"--cwd", "./packages/app", "test", "--watch=false"}})
	if dir != "./packages/app" {
		t.Fatalf("dir = %q, want ./packages/app", dir)
	}
	want := Invocation{Executable: "yarn", Args: []string{"test", "--watch=false"}}
	if !invocationsEqual(got, want) {
		t.Fatalf("canonical = %+v, want %+v", got, want)
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
	return a.Executable == b.Executable && slices.Equal(a.Args, b.Args)
}
