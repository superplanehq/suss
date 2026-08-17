package plan

import "testing"

func TestApplyWorkspaceScopeMarksRootCommands(t *testing.T) {
	t.Parallel()

	project := NewProjectPlan(".")
	project.Facts = []ProjectFact{{Name: "workspace.orchestrator", Value: "pnpm"}}
	project.Commands = []Command{{Name: "test", Scope: ScopeProject}}
	project.Preparation = []Command{{Name: "install dependencies", Scope: ScopeProject}}

	project.ApplyWorkspaceScope()

	if project.Commands[0].Scope != ScopeRepository {
		t.Fatalf("command scope = %q, want repository", project.Commands[0].Scope)
	}
	if project.Preparation[0].Scope != ScopeRepository {
		t.Fatalf("preparation scope = %q, want repository", project.Preparation[0].Scope)
	}
}
