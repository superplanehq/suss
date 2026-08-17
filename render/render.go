// Package render formats a plan document for humans. It does not inspect the
// repository; every line is derived from the JSON document plus the list of
// providers that produced it.
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
	Providers []string
}

// toolCapabilities maps configured-tool fact values to the capabilities that
// would satisfy them. This duplicates capability facts in
// knowledge/invocations.json and can drift; derive both from one source when
// the knowledge base grows.
var toolCapabilities = map[string][]plan.Capability{
	"eslint":   {plan.CapabilityCodeLint},
	"prettier": {plan.CapabilityCodeFormat},
	"tsc":      {plan.CapabilityCodeTypecheck},
	"vitest":   {plan.CapabilityTestRun},
	"jest":     {plan.CapabilityTestRun},
	"vite":     {plan.CapabilityArtifactBuild, plan.CapabilityApplicationRun},
}

// Write prints a human-readable rendering of document.
func Write(w io.Writer, document plan.Document, opts Options) {
	fmt.Fprintf(w, "Providers: %s\n", joinProviders(opts.Providers))
	fmt.Fprintln(w)

	if len(document.Projects) == 0 {
		fmt.Fprintln(w, "No project roots were detected. Suss looks for package.json, go.mod, and mix.exs.")
		return
	}

	for i, project := range document.Projects {
		if i > 0 {
			fmt.Fprintln(w)
		}
		writeProject(w, project, opts.Providers)
	}
}

func joinProviders(names []string) string {
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
}

func writeProject(w io.Writer, project plan.ProjectPlan, providers []string) {
	fmt.Fprintf(w, "Project: %s\n", project.Path)
	fmt.Fprintf(w, "Path: %s\n", project.Path)

	if !claimed(project) {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "No implemented provider produced findings for this project. Providers that ran: %s. A Node project requires package.json.\n", joinProviders(providers))
		writeFacts(w, project.Facts)
		writeEvidence(w, project)
		return
	}

	if len(project.Languages) > 0 {
		fmt.Fprintf(w, "Languages: %s\n", joinDetected(project.Languages))
	}
	if len(project.Frameworks) > 0 {
		fmt.Fprintf(w, "Frameworks: %s\n", joinDetected(project.Frameworks))
	}
	if len(project.PackageManagers) > 0 {
		fmt.Fprintf(w, "Package managers: %s\n", joinTools(project.PackageManagers))
	}
	writeFacts(w, project.Facts)
	writeNamedList(w, "Requirements", requirementLines(project.Requirements))
	writeCommands(w, "Preparation", project.Preparation)
	writeCommands(w, "Commands", project.Commands)
	writeAmbiguities(w, project.Ambiguities)
	writeConflicts(w, project.Conflicts)
	writeConfiguredWithoutCommand(w, project)
	writeEvidence(w, project)
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

func writeFacts(w io.Writer, facts []plan.ProjectFact) {
	var lines []string
	for _, fact := range facts {
		if fact.Name == "tool.configured" {
			continue
		}
		lines = append(lines, fmt.Sprintf("  %s: %s", fact.Name, fact.Value))
	}
	writeNamedList(w, "Facts", lines)
}

func requirementLines(requirements []plan.Requirement) []string {
	lines := make([]string, 0, len(requirements))
	for _, requirement := range requirements {
		line := "  " + requirement.Name
		if requirement.Version != "" {
			line += " " + requirement.Version
		}
		lines = append(lines, line)
	}
	return lines
}

func writeCommands(w io.Writer, title string, commands []plan.Command) {
	if len(commands) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s:\n", title)
	width := commandNameWidth(commands)
	for _, command := range commands {
		run := "(unresolved invocation)"
		if command.Run != nil {
			run = *command.Run
		}
		fmt.Fprintf(w, "  %-*s  %s%s\n", width, command.Name, run, capabilitySuffix(command))
	}
}

func commandNameWidth(commands []plan.Command) int {
	width := 0
	for _, command := range commands {
		if len(command.Name) > width {
			width = len(command.Name)
		}
	}
	return width
}

func capabilitySuffix(command plan.Command) string {
	if len(command.Interpretations) == 0 {
		return ""
	}
	caps := make([]string, 0, len(command.Interpretations))
	for _, interpretation := range command.Interpretations {
		caps = append(caps, string(interpretation.Capability))
	}
	return "  (" + strings.Join(caps, ", ") + ")"
}

func writeAmbiguities(w io.Writer, ambiguities []plan.Ambiguity) {
	if len(ambiguities) == 0 {
		return
	}
	fmt.Fprintln(w, "\nAmbiguities:")
	for _, ambiguity := range ambiguities {
		fmt.Fprintf(w, "  %s\n", ambiguity.Subject)
		fmt.Fprintf(w, "    %s\n", ambiguity.Message)
		for _, candidate := range ambiguity.Candidates {
			fmt.Fprintf(w, "    - %s\n", candidate.Value)
		}
	}
}

func writeConflicts(w io.Writer, conflicts []plan.Conflict) {
	if len(conflicts) == 0 {
		return
	}
	fmt.Fprintln(w, "\nConflicts:")
	for _, conflict := range conflicts {
		fmt.Fprintf(w, "  %s\n", conflict.Subject)
		fmt.Fprintf(w, "    %s\n", conflict.Message)
		for _, assertion := range conflict.Assertions {
			fmt.Fprintf(w, "    - %s\n", assertion.Value)
		}
		if conflict.Resolution != nil {
			fmt.Fprintf(w, "    Selected: %s (%s)\n", conflict.Resolution.SelectedValue, conflict.Resolution.Reason)
		}
	}
}

func writeConfiguredWithoutCommand(w io.Writer, project plan.ProjectPlan) {
	var lines []string
	for _, fact := range project.Facts {
		if fact.Name != "tool.configured" {
			continue
		}
		capabilities, ok := toolCapabilities[fact.Value]
		if !ok {
			continue
		}
		if hasAnyCapability(project, capabilities) {
			continue
		}
		lines = append(lines, fmt.Sprintf("  %s is configured. No command interpreted as %s was found.", fact.Value, joinCapabilities(capabilities)))
	}
	writeNamedList(w, "Configured tools without a matching command", lines)
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
	fmt.Fprintln(w, "\nEvidence:")
	for _, source := range sources {
		fmt.Fprintf(w, "  %s\n", source)
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

func writeNamedList(w io.Writer, title string, lines []string) {
	if len(lines) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s:\n", title)
	for _, line := range lines {
		fmt.Fprintln(w, line)
	}
}
