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

func invocationsEqual(a, b Invocation) bool {
	return a.Executable == b.Executable && slices.Equal(a.Args, b.Args)
}
