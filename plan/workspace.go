package plan

// HasWorkspaceOrchestrator reports whether the project declared a workspace
// orchestrator fact (pnpm, yarn, turbo, nx).
func (p ProjectPlan) HasWorkspaceOrchestrator() bool {
	for _, fact := range p.Facts {
		if fact.Name == "workspace.orchestrator" {
			return true
		}
	}
	return false
}

// ApplyWorkspaceScope marks root commands as repository-scoped when the
// project is a workspace orchestrator.
func (p *ProjectPlan) ApplyWorkspaceScope() {
	if !p.HasWorkspaceOrchestrator() {
		return
	}
	for i := range p.Commands {
		p.Commands[i].Scope = ScopeRepository
	}
	for i := range p.Preparation {
		p.Preparation[i].Scope = ScopeRepository
	}
}
