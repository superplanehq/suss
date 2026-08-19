package render

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/superplanehq/suss/plan"
)

type displayedCommand struct {
	command plan.Command
	purpose string
	rank    int
}

type commandRow struct {
	label string
	run   string
}

func writeActionableCommands(w io.Writer, project plan.ProjectPlan) {
	commands := actionableCommands(project)
	if len(commands) == 0 {
		return
	}

	fmt.Fprintln(w, "\n  How to work with this project:")
	writeCommandTable(w, "Purpose", actionableRows(project.Path, commands))
}

func actionableCommands(project plan.ProjectPlan) []displayedCommand {
	commands := make([]displayedCommand, 0, len(project.Preparation)+len(project.Commands))
	for _, command := range project.Preparation {
		purpose, rank := commandPurpose(command)
		if purpose == "" {
			purpose = "Prepare"
			rank = 0
		}
		commands = append(commands, displayedCommand{command: command, purpose: purpose, rank: rank})
	}
	for _, command := range project.Commands {
		purpose, rank := commandPurpose(command)
		if purpose == "" {
			continue
		}
		commands = append(commands, displayedCommand{command: command, purpose: purpose, rank: rank})
	}
	slices.SortStableFunc(commands, func(a, b displayedCommand) int {
		if n := cmp.Compare(a.rank, b.rank); n != 0 {
			return n
		}
		if n := cmp.Compare(a.purpose, b.purpose); n != 0 {
			return n
		}
		return cmp.Compare(a.command.Name, b.command.Name)
	})
	return preferredCommands(commands)
}

func preferredCommands(commands []displayedCommand) []displayedCommand {
	preferred := make([]displayedCommand, 0, len(commands))
	indexes := make(map[string]int)
	for _, candidate := range commands {
		index, found := indexes[candidate.purpose]
		if !found {
			indexes[candidate.purpose] = len(preferred)
			preferred = append(preferred, candidate)
			continue
		}
		if compareCommandPreference(candidate, preferred[index]) < 0 {
			preferred[index] = candidate
		}
	}
	return preferred
}

func compareCommandPreference(a, b displayedCommand) int {
	if n := cmp.Compare(commandOriginRank(a.command.Origin), commandOriginRank(b.command.Origin)); n != 0 {
		return n
	}
	if n := cmp.Compare(commandNameRank(a.purpose, a.command.Name), commandNameRank(b.purpose, b.command.Name)); n != 0 {
		return n
	}
	if n := cmp.Compare(len(derefRun(a.command.Run)), len(derefRun(b.command.Run))); n != 0 {
		return n
	}
	if n := cmp.Compare(a.command.Name, b.command.Name); n != 0 {
		return n
	}
	return cmp.Compare(string(a.command.ID), string(b.command.ID))
}

func commandOriginRank(origin plan.CommandOrigin) int {
	switch origin {
	case plan.CommandDeclared:
		return 0
	case plan.CommandInferred:
		return 1
	case plan.CommandObserved:
		return 2
	default:
		return 3
	}
}

func commandNameRank(purpose, name string) int {
	name = strings.ToLower(strings.TrimSpace(name))
	preferred := map[string][]string{
		"Install dependencies": {"deps", "dependencies", "install", "setup", "node_modules"},
		"Build":                {"build", "compile"},
		"Test":                 {"test", "tests", "spec"},
		"Lint":                 {"lint", "vet"},
		"Format":               {"format", "fmt"},
		"Type-check":           {"typecheck", "type-check"},
		"Run":                  {"run", "start", "serve", "dev"},
	}
	if slices.Contains(preferred[purpose], name) {
		return 0
	}
	if purpose == "Format" && (strings.Contains(name, ":check") || strings.Contains(name, "-check")) {
		return 1
	}
	return 2
}

func commandPurpose(command plan.Command) (string, int) {
	capabilities := make([]plan.Capability, 0, len(command.Interpretations))
	for _, interpretation := range command.Interpretations {
		capabilities = append(capabilities, interpretation.Capability)
	}
	if len(capabilities) == 0 {
		return "", 0
	}
	slices.SortFunc(capabilities, func(a, b plan.Capability) int {
		if n := cmp.Compare(capabilityRank(a), capabilityRank(b)); n != 0 {
			return n
		}
		return cmp.Compare(string(a), string(b))
	})

	labels := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		labels = append(labels, capabilityLabel(capability))
	}
	return strings.Join(labels, ", "), capabilityRank(capabilities[0])
}

func capabilityRank(capability plan.Capability) int {
	switch capability {
	case plan.CapabilityDependenciesInstall:
		return 0
	case plan.CapabilityArtifactBuild:
		return 1
	case plan.CapabilityTestRun:
		return 2
	case plan.CapabilityCodeLint:
		return 3
	case plan.CapabilityCodeFormat:
		return 4
	case plan.CapabilityCodeTypecheck:
		return 5
	case plan.CapabilityApplicationRun:
		return 6
	default:
		return 7
	}
}

func capabilityLabel(capability plan.Capability) string {
	switch capability {
	case plan.CapabilityDependenciesInstall:
		return "Install dependencies"
	case plan.CapabilityArtifactBuild:
		return "Build"
	case plan.CapabilityTestRun:
		return "Test"
	case plan.CapabilityCodeLint:
		return "Lint"
	case plan.CapabilityCodeFormat:
		return "Format"
	case plan.CapabilityCodeTypecheck:
		return "Type-check"
	case plan.CapabilityApplicationRun:
		return "Run"
	default:
		return string(capability)
	}
}

func actionableRows(projectPath string, commands []displayedCommand) []commandRow {
	var rows []commandRow
	for _, item := range commands {
		rows = append(rows, commandRow{
			label: item.purpose,
			run:   commandRun(projectPath, item.command.Run, item.command.Directory),
		})
		rows = append(rows, variantRows(projectPath, item.command)...)
	}
	return uniqueCommandRows(rows)
}

func uniqueCommandRows(rows []commandRow) []commandRow {
	unique := make([]commandRow, 0, len(rows))
	seen := make(map[commandRow]struct{}, len(rows))
	for _, row := range rows {
		if _, ok := seen[row]; ok {
			continue
		}
		seen[row] = struct{}{}
		unique = append(unique, row)
	}
	return unique
}

func commandRun(projectPath string, run *string, directory string) string {
	text := "(unresolved invocation)"
	if run != nil {
		text = oneLine(*run)
	}
	if note := commandDirectoryNote(projectPath, directory); note != "" {
		text += "  (" + note + ")"
	}
	return text
}

func variantRows(projectPath string, command plan.Command) []commandRow {
	primaryRun := commandRun(projectPath, command.Run, command.Directory)
	rows := make([]commandRow, 0, len(command.Variants))
	for _, variant := range command.Variants {
		run := commandRun(projectPath, &variant.Run, variant.Directory)
		if run == primaryRun {
			continue
		}
		context := oneLine(variant.Context)
		if strings.EqualFold(context, "ci") {
			context = "CI"
		}
		rows = append(rows, commandRow{label: "  " + context + " variant", run: run})
	}
	return rows
}

func writeUninterpretedCommands(w io.Writer, projectPath string, commands []plan.Command) {
	commands = uninterpretedCommands(commands)
	if len(commands) == 0 {
		return
	}

	fmt.Fprintln(w, "\n  Uninterpreted commands:")
	var rows []commandRow
	for _, command := range commands {
		rows = append(rows, commandRow{
			label: oneLine(command.Name),
			run:   commandRun(projectPath, command.Run, command.Directory),
		})
		rows = append(rows, variantRows(projectPath, command)...)
	}
	writeCommandTable(w, "Name", rows)
}

func uninterpretedCommands(commands []plan.Command) []plan.Command {
	var out []plan.Command
	for _, command := range commands {
		if len(command.Interpretations) == 0 {
			out = append(out, command)
		}
	}
	slices.SortStableFunc(out, func(a, b plan.Command) int {
		return cmp.Compare(a.Name, b.Name)
	})
	return out
}

func writeCommandTable(w io.Writer, labelHeading string, rows []commandRow) {
	width := len(labelHeading)
	for _, row := range rows {
		if len(row.label) > width {
			width = len(row.label)
		}
	}
	const commandHeading = "Command"
	fmt.Fprintf(w, "    %-*s  %s\n", width, labelHeading, commandHeading)
	fmt.Fprintf(w, "    %s  %s\n", strings.Repeat("-", width), strings.Repeat("-", len(commandHeading)))
	for _, row := range rows {
		fmt.Fprintf(w, "    %-*s  %s\n", width, row.label, row.run)
	}
}

func commandDirectoryNote(projectPath, directory string) string {
	projectPath = strings.TrimSpace(projectPath)
	directory = strings.TrimSpace(directory)
	if directory == "" || directory == "." || directory == projectPath {
		return ""
	}
	return "in " + directory
}

func oneLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
