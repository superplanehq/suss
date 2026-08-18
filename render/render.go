// Package render formats a plan document for humans. It does not inspect the
// repository; every line is derived from the JSON document plus renderer
// options such as provider names and the repository directory name.
package render

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/superplanehq/suss/plan"
)

// Options control labels that are not stored in the document itself.
type Options struct {
	Providers         []string
	RepositoryName    string
	ShowUninterpreted bool
	ShowEvidence      bool
}

// toolCapabilities maps configured-tool fact values to the capabilities that
// would satisfy them. This duplicates capability facts in
// knowledge/invocations.json and can drift; derive both from one source when
// the knowledge base grows.
var toolCapabilities = map[string][]plan.Capability{
	"eslint":        {plan.CapabilityCodeLint},
	"prettier":      {plan.CapabilityCodeFormat},
	"tsc":           {plan.CapabilityCodeTypecheck},
	"vitest":        {plan.CapabilityTestRun},
	"jest":          {plan.CapabilityTestRun},
	"vite":          {plan.CapabilityArtifactBuild, plan.CapabilityApplicationRun},
	"golangci-lint": {plan.CapabilityCodeLint},
	"rspec":         {plan.CapabilityTestRun},
	"rubocop":       {plan.CapabilityCodeLint},
	"standard":      {plan.CapabilityCodeLint, plan.CapabilityCodeFormat},
	"sorbet":        {plan.CapabilityCodeTypecheck},
	"phpunit":       {plan.CapabilityTestRun},
	"pest":          {plan.CapabilityTestRun},
	"phpstan":       {plan.CapabilityCodeTypecheck},
	"psalm":         {plan.CapabilityCodeTypecheck},
	"php-cs-fixer":  {plan.CapabilityCodeFormat, plan.CapabilityCodeLint},
	"phpcs":         {plan.CapabilityCodeLint},
	"pint":          {plan.CapabilityCodeLint, plan.CapabilityCodeFormat},
	"clippy":        {plan.CapabilityCodeLint},
	"rustfmt":       {plan.CapabilityCodeFormat},
	"nextest":       {plan.CapabilityTestRun},
}

type classifiedProjects struct {
	primary         []plan.ProjectPlan
	examples        []plan.ProjectPlan
	omittedFixtures int
}

// Write prints a human-readable rendering of document.
func Write(w io.Writer, document plan.Document, opts Options) {
	classified := classifyProjects(document.Projects)
	writePreface(w, len(classified.primary), classified.omittedFixtures)

	if len(document.Projects) == 0 {
		fmt.Fprintln(w, "No project roots were detected. Suss looks for package.json, go.mod, mix.exs, Gemfile, composer.json, Makefile, and .env.example.")
		return
	}
	if len(classified.primary) == 0 && len(classified.examples) == 0 {
		fmt.Fprintln(w, "No non-fixture project roots were detected.")
		return
	}

	visible := classified.primary
	if len(visible) == 0 {
		visible = classified.examples
	}
	for i, project := range visible {
		if i > 0 {
			fmt.Fprintln(w)
			fmt.Fprintln(w)
		}
		writeProject(w, project, opts)
	}
	if len(classified.primary) > 0 {
		writeExampleIndex(w, classified.examples)
	}
}

func classifyProjects(projects []plan.ProjectPlan) classifiedProjects {
	var classified classifiedProjects
	for _, project := range projects {
		confidence, isFixture := fixtureRole(project)
		if !isFixture {
			classified.primary = append(classified.primary, project)
			continue
		}
		if confidence == plan.ConfidenceHigh {
			classified.omittedFixtures++
			continue
		}
		classified.examples = append(classified.examples, project)
	}
	return classified
}

func fixtureRole(project plan.ProjectPlan) (plan.Confidence, bool) {
	for _, fact := range project.Facts {
		if fact.Name == "project.role" && fact.Value == "fixture" {
			return fact.Confidence, true
		}
	}
	return "", false
}

func writePreface(w io.Writer, primaryCount, omittedFixtures int) {
	switch {
	case primaryCount > 1 && omittedFixtures > 0:
		fmt.Fprintf(w, "Projects: %d (%d %s omitted; use --json to inspect)\n\n", primaryCount, omittedFixtures, fixtureNoun(omittedFixtures))
	case primaryCount > 1:
		fmt.Fprintf(w, "Projects: %d\n\n", primaryCount)
	case omittedFixtures > 0:
		fmt.Fprintf(w, "%d %s omitted; use --json to inspect\n\n", omittedFixtures, fixtureNoun(omittedFixtures))
	}
}

func fixtureNoun(count int) string {
	if count == 1 {
		return "fixture project"
	}
	return "fixture projects"
}

func joinProviders(names []string) string {
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
}

func writeProject(w io.Writer, project plan.ProjectPlan, opts Options) {
	heading := projectHeading(project, opts)
	fmt.Fprintln(w, heading)
	fmt.Fprintln(w, strings.Repeat("=", len(heading)))

	if !claimed(project) {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "  No implemented provider produced findings for this project. Providers that ran: %s. A Node project requires package.json; a Go project requires go.mod.\n", joinProviders(opts.Providers))
		writeProjectDetails(w, project)
		if opts.ShowEvidence {
			writeEvidence(w, project)
		}
		return
	}

	writeActionableCommands(w, project)
	writeAttentionItems(w, project.Ambiguities, project.Conflicts)
	if opts.ShowUninterpreted {
		writeUninterpretedCommands(w, project.Path, project.Commands)
	}
	writeProjectDetails(w, project)
	if opts.ShowEvidence {
		writeEvidence(w, project)
	}
}

func projectHeading(project plan.ProjectPlan, opts Options) string {
	if project.Path == "." {
		return rootHeading(opts.RepositoryName)
	}
	if _, isExample := fixtureRole(project); isExample {
		return "Example: " + project.Path
	}
	return "Project: " + project.Path
}

func rootHeading(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || name == "." {
		return "Repository root"
	}
	return name
}

func writeExampleIndex(w io.Writer, examples []plan.ProjectPlan) {
	if len(examples) == 0 {
		return
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w)
	if len(examples) == 1 {
		fmt.Fprintln(w, "Example project:")
	} else {
		fmt.Fprintln(w, "Example projects:")
	}
	for _, project := range examples {
		fmt.Fprintf(w, "  %s\n", exampleIndexLine(project))
	}
}

func exampleIndexLine(project plan.ProjectPlan) string {
	var parts []string
	if names := joinDetected(project.Languages); names != "" {
		parts = append(parts, names)
	}
	if names := joinTools(project.PackageManagers); names != "" {
		parts = append(parts, names)
	}
	if len(parts) == 0 {
		return project.Path
	}
	return project.Path + "  (" + strings.Join(parts, ", ") + ")"
}

func claimed(project plan.ProjectPlan) bool {
	if len(project.Languages) > 0 || len(project.Frameworks) > 0 || len(project.PackageManagers) > 0 {
		return true
	}
	if len(project.Requirements) > 0 || len(project.Preparation) > 0 || len(project.Commands) > 0 {
		return true
	}
	for _, fact := range project.Facts {
		if fact.Name != "project.role" {
			return true
		}
	}
	return false
}

func joinDetected(values []plan.DetectedValue) string {
	names := make([]string, 0, len(values))
	for _, value := range values {
		names = append(names, value.Name)
	}
	return strings.Join(names, ", ")
}

func joinTools(tools []plan.DetectedTool) string {
	parts := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool.Version != "" {
			parts = append(parts, tool.Name+" "+tool.Version)
			continue
		}
		parts = append(parts, tool.Name)
	}
	return strings.Join(parts, ", ")
}

func requirementLines(requirements []plan.Requirement) []string {
	lines := make([]string, 0, len(requirements))
	for _, requirement := range requirements {
		line := requirementKindLabel(requirement.Kind) + " " + requirement.Name
		if requirement.Version != "" {
			line += " " + requirement.Version
		}
		if requirement.Kind == plan.RequirementEnvironment {
			var notes []string
			if requirement.IsRequired != nil && *requirement.IsRequired {
				notes = append(notes, "required")
			}
			if requirement.HasDefault != nil && *requirement.HasDefault {
				notes = append(notes, "default present")
			}
			if len(notes) > 0 {
				line += " (" + strings.Join(notes, ", ") + ")"
			}
		}
		lines = append(lines, line)
	}
	return lines
}

func requirementKindLabel(kind plan.RequirementKind) string {
	switch kind {
	case plan.RequirementRuntime:
		return "runtime"
	case plan.RequirementTool:
		return "tool"
	case plan.RequirementService:
		return "service"
	case plan.RequirementEnvironment:
		return "environment"
	default:
		return string(kind)
	}
}

func writeAttentionItems(w io.Writer, ambiguities []plan.Ambiguity, conflicts []plan.Conflict) {
	if len(ambiguities) == 0 && len(conflicts) == 0 {
		return
	}
	fmt.Fprintln(w, "\n  Needs attention:")
	for _, ambiguity := range ambiguities {
		fmt.Fprintf(w, "    Ambiguity: %s\n", ambiguity.Subject)
		fmt.Fprintf(w, "      %s\n", ambiguity.Message)
		for _, candidate := range ambiguity.Candidates {
			fmt.Fprintf(w, "      - %s\n", candidate.Value)
		}
	}
	for _, conflict := range conflicts {
		fmt.Fprintf(w, "    Conflict: %s\n", conflict.Subject)
		fmt.Fprintf(w, "      %s\n", conflict.Message)
		for _, assertion := range conflict.Assertions {
			fmt.Fprintf(w, "      - %s\n", assertion.Value)
		}
		if conflict.Resolution != nil {
			fmt.Fprintf(w, "      Selected: %s (%s)\n", conflict.Resolution.SelectedValue, conflict.Resolution.Reason)
		}
	}
}

func writeProjectDetails(w io.Writer, project plan.ProjectPlan) {
	facts := projectFactLines(project.Facts)
	configuredTools := configuredWithoutCommandLines(project)
	if len(project.Languages) == 0 && len(project.Frameworks) == 0 && len(project.PackageManagers) == 0 &&
		len(project.Requirements) == 0 && len(facts) == 0 && len(configuredTools) == 0 {
		return
	}

	fmt.Fprintln(w, "\n  Project details:")
	if len(project.Languages) > 0 {
		fmt.Fprintf(w, "    Languages: %s\n", joinDetected(project.Languages))
	}
	if len(project.Frameworks) > 0 {
		fmt.Fprintf(w, "    Frameworks: %s\n", joinDetected(project.Frameworks))
	}
	if len(project.PackageManagers) > 0 {
		fmt.Fprintf(w, "    Package managers: %s\n", joinTools(project.PackageManagers))
	}
	writeDetailList(w, "Requirements", requirementLines(project.Requirements))
	writeDetailList(w, "Facts", facts)
	writeDetailList(w, "Configured tools without a matching command", configuredTools)
}

func projectFactLines(facts []plan.ProjectFact) []string {
	var lines []string
	for _, fact := range facts {
		if fact.Name != "tool.configured" {
			lines = append(lines, fmt.Sprintf("%s: %s", fact.Name, fact.Value))
		}
	}
	return lines
}

func writeDetailList(w io.Writer, title string, lines []string) {
	if len(lines) == 0 {
		return
	}
	fmt.Fprintf(w, "    %s:\n", title)
	for _, line := range lines {
		fmt.Fprintf(w, "      %s\n", line)
	}
}

func configuredWithoutCommandLines(project plan.ProjectPlan) []string {
	var lines []string
	for _, fact := range project.Facts {
		if fact.Name != "tool.configured" {
			continue
		}
		capabilities, ok := toolCapabilities[fact.Value]
		if !ok {
			lines = append(lines, fmt.Sprintf("%s is configured.", fact.Value))
			continue
		}
		if hasAnyCapability(project, capabilities) {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s is configured. No command interpreted as %s was found.", fact.Value, joinCapabilities(capabilities)))
	}
	return lines
}

func hasAnyCapability(project plan.ProjectPlan, capabilities []plan.Capability) bool {
	commands := append(append([]plan.Command{}, project.Preparation...), project.Commands...)
	for _, command := range commands {
		for _, interpretation := range command.Interpretations {
			if slices.Contains(capabilities, interpretation.Capability) {
				return true
			}
		}
	}
	return false
}

func joinCapabilities(capabilities []plan.Capability) string {
	parts := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		parts = append(parts, string(capability))
	}
	return strings.Join(parts, " or ")
}

func writeEvidence(w io.Writer, project plan.ProjectPlan) {
	sources := uniqueSources(project)
	if len(sources) == 0 {
		return
	}
	fmt.Fprintln(w, "\n  Evidence:")
	for _, source := range sources {
		fmt.Fprintf(w, "    %s\n", source)
	}
}

func uniqueSources(project plan.ProjectPlan) []string {
	seen := make(map[string]struct{})
	visit := func(evidence []plan.Evidence) {
		for _, item := range evidence {
			if item.Kind == plan.EvidenceConvention {
				continue
			}
			seen[item.Source] = struct{}{}
		}
	}

	for _, value := range project.Languages {
		visit(value.Evidence)
	}
	for _, value := range project.Frameworks {
		visit(value.Evidence)
	}
	for _, tool := range project.PackageManagers {
		visit(tool.Evidence)
	}
	for _, fact := range project.Facts {
		visit(fact.Evidence)
	}
	for _, requirement := range project.Requirements {
		visit(requirement.Evidence)
	}
	for _, command := range append(append([]plan.Command{}, project.Preparation...), project.Commands...) {
		visit(command.Evidence)
		for _, interpretation := range command.Interpretations {
			visit(interpretation.Evidence)
		}
		for _, variant := range command.Variants {
			visit(variant.Evidence)
		}
	}
	for _, ambiguity := range project.Ambiguities {
		for _, candidate := range ambiguity.Candidates {
			visit(candidate.Evidence)
		}
	}
	for _, conflict := range project.Conflicts {
		for _, assertion := range conflict.Assertions {
			visit(assertion.Evidence)
		}
		if conflict.Resolution != nil {
			visit(conflict.Resolution.Evidence)
		}
	}

	sources := make([]string, 0, len(seen))
	for source := range seen {
		sources = append(sources, source)
	}
	slices.Sort(sources)
	return sources
}
