package render

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/superplanehq/suss/plan"
)

const composeEnvironmentPreviewLimit = 3

type composeEnvironmentSummary struct {
	directory            string
	run                  string
	services             []string
	environmentVariables []string
}

func summarizeComposeEnvironments(project plan.ProjectPlan) (plan.ProjectPlan, []composeEnvironmentSummary) {
	var summaries []composeEnvironmentSummary
	composeSources := make(map[string][]int)
	preparation := make([]plan.Command, 0, len(project.Preparation))

	for _, command := range project.Preparation {
		if !isComposeEnvironmentCommand(command) {
			preparation = append(preparation, command)
			continue
		}
		index := len(summaries)
		summaries = append(summaries, composeEnvironmentSummary{
			directory: command.Directory,
			run:       oneLine(derefRun(command.Run)),
		})
		for _, evidence := range command.Evidence {
			if evidence.Kind == plan.EvidenceFile && evidence.Source != "" {
				composeSources[evidence.Source] = append(composeSources[evidence.Source], index)
			}
		}
	}
	if len(summaries) == 0 {
		return project, nil
	}

	requirements := make([]plan.Requirement, 0, len(project.Requirements))
	for _, requirement := range project.Requirements {
		matched := matchingComposeEnvironments(requirement.Evidence, composeSources)
		for index := range matched {
			switch requirement.Kind {
			case plan.RequirementService:
				summaries[index].services = append(summaries[index].services, requirementLabel(requirement))
			case plan.RequirementEnvironment:
				summaries[index].environmentVariables = append(summaries[index].environmentVariables, requirement.Name)
			}
		}
		if isComposeOnlyRequirement(requirement, composeSources) {
			continue
		}
		requirements = append(requirements, requirement)
	}

	project.Preparation = preparation
	project.Requirements = requirements
	return project, summaries
}

func requirementLabel(requirement plan.Requirement) string {
	if requirement.Version == "" || strings.HasPrefix(requirement.Version, "sha256:") {
		return requirement.Name
	}
	return requirement.Name + " " + requirement.Version
}

func isComposeEnvironmentCommand(command plan.Command) bool {
	for _, evidence := range command.Evidence {
		if evidence.Kind == plan.EvidenceConvention && evidence.Source == "compose" && evidence.Pointer == "up" {
			return true
		}
	}
	return false
}

func matchingComposeEnvironments(evidence []plan.Evidence, composeSources map[string][]int) map[int]struct{} {
	matched := make(map[int]struct{})
	for _, item := range evidence {
		for _, index := range composeSources[item.Source] {
			matched[index] = struct{}{}
		}
	}
	return matched
}

func isComposeOnlyRequirement(requirement plan.Requirement, composeSources map[string][]int) bool {
	if requirement.Kind != plan.RequirementService && requirement.Kind != plan.RequirementEnvironment {
		return false
	}
	matched := false
	for _, evidence := range requirement.Evidence {
		if len(composeSources[evidence.Source]) == 0 {
			return false
		}
		matched = true
	}
	return matched
}

func writeComposeEnvironmentPreview(w io.Writer, summaries []composeEnvironmentSummary) {
	if len(summaries) == 0 {
		return
	}
	writeComposeHeading(w, len(summaries))

	if len(summaries) <= composeEnvironmentPreviewLimit {
		for _, summary := range summaries {
			writeComposeEnvironmentDetails(w, summary)
		}
		return
	}

	visible := mostInformativeComposeEnvironments(summaries)
	for _, summary := range visible {
		writeComposeEnvironmentPreviewItem(w, summary)
	}
	if omitted := len(summaries) - len(visible); omitted > 0 {
		fmt.Fprintf(w, "    %d more %s omitted; use --all-environments to inspect.\n", omitted, environmentNoun(omitted))
	}
}

func mostInformativeComposeEnvironments(summaries []composeEnvironmentSummary) []composeEnvironmentSummary {
	ordered := append([]composeEnvironmentSummary{}, summaries...)
	slices.SortStableFunc(ordered, func(a, b composeEnvironmentSummary) int {
		if n := cmp.Compare(len(b.services), len(a.services)); n != 0 {
			return n
		}
		if n := cmp.Compare(len(b.environmentVariables), len(a.environmentVariables)); n != 0 {
			return n
		}
		return cmp.Compare(a.directory, b.directory)
	})
	return ordered[:composeEnvironmentPreviewLimit]
}

func writeAllComposeEnvironments(w io.Writer, summaries []composeEnvironmentSummary) {
	if len(summaries) == 0 {
		return
	}
	writeComposeHeading(w, len(summaries))
	for _, summary := range summaries {
		writeComposeEnvironmentDetails(w, summary)
	}
}

func writeComposeHeading(w io.Writer, count int) {
	if count == 1 {
		fmt.Fprintln(w, "\n  Compose environment:")
		return
	}
	fmt.Fprintf(w, "\n  Compose environments: %d\n", count)
}

func writeComposeEnvironmentPreviewItem(w io.Writer, summary composeEnvironmentSummary) {
	fmt.Fprintf(w, "    %s\n", summary.directory)
	fmt.Fprintf(w, "      Start: %s\n", summary.run)
	fmt.Fprintf(w, "      Services: %s\n", compactList(summary.services, 5))
	fmt.Fprintf(w, "      Environment variables: %d\n", len(summary.environmentVariables))
}

func writeComposeEnvironmentDetails(w io.Writer, summary composeEnvironmentSummary) {
	fmt.Fprintf(w, "    %s\n", summary.directory)
	fmt.Fprintf(w, "      Start: %s\n", summary.run)
	fmt.Fprintf(w, "      Services: %s\n", joinOrNone(summary.services))
	fmt.Fprintf(w, "      Environment variables: %s\n", joinOrNone(summary.environmentVariables))
}

func compactList(values []string, limit int) string {
	if len(values) <= limit {
		return joinOrNone(values)
	}
	return fmt.Sprintf("%s (+%d more)", strings.Join(values[:limit], ", "), len(values)-limit)
}

func joinOrNone(values []string) string {
	if len(values) == 0 {
		return "none detected"
	}
	return strings.Join(values, ", ")
}

func environmentNoun(count int) string {
	if count == 1 {
		return "environment"
	}
	return "environments"
}
