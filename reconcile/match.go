package reconcile

import (
	"cmp"
	"slices"
	"strings"

	"github.com/superplanehq/suss/knowledge"
	"github.com/superplanehq/suss/plan"
)

type matchKind int

const (
	matchNone matchKind = iota
	matchVariant
	matchConflict
)

type matchResult struct {
	kind    matchKind
	command plan.Command
}

func match(observed plan.Command, existing []plan.Command) matchResult {
	var variants []plan.Command
	var conflicts []plan.Command
	for _, candidate := range existing {
		if candidate.Origin == plan.CommandObserved {
			continue
		}
		switch classifyPair(observed, candidate) {
		case matchVariant:
			variants = append(variants, candidate)
		case matchConflict:
			conflicts = append(conflicts, candidate)
		}
	}
	if len(variants) > 0 {
		return matchResult{kind: matchVariant, command: pickVariant(variants)}
	}
	if len(conflicts) > 0 {
		return matchResult{kind: matchConflict, command: pickVariant(conflicts)}
	}
	return matchResult{kind: matchNone}
}

func classifyPair(observed, existing plan.Command) matchKind {
	if normalizeDir(observed.Directory) != normalizeDir(existing.Directory) {
		return matchNone
	}
	if existing.Run == nil || observed.Run == nil {
		return matchNone
	}

	observedInv, ok := invocationOf(observed)
	if !ok {
		return matchNone
	}
	existingInv, ok := invocationOf(existing)
	if !ok {
		return matchNone
	}

	observedPM, observedIsPM := knowledge.ClassifyManager(observedInv)
	existingPM, existingIsPM := knowledge.ClassifyManager(existingInv)
	if observedIsPM && existingIsPM {
		return classifyManagerPair(observedPM, existingPM)
	}

	if observedInv.Executable == "" || observedInv.Executable != existingInv.Executable {
		return matchNone
	}
	if hasArgsPrefix(existingInv.Args, observedInv.Args) {
		return matchVariant
	}
	return matchNone
}

func classifyManagerPair(observed, existing knowledge.ManagerInvocation) matchKind {
	if observed.Install && existing.Install {
		if observed.Manager == existing.Manager {
			return matchVariant
		}
		return matchConflict
	}
	if observed.Script == "" || observed.Script != existing.Script {
		return matchNone
	}
	if observed.Manager != existing.Manager {
		return matchConflict
	}
	return matchVariant
}

func pickVariant(commands []plan.Command) plan.Command {
	slices.SortFunc(commands, func(a, b plan.Command) int {
		if n := originRank(a.Origin) - originRank(b.Origin); n != 0 {
			return n
		}
		return cmp.Compare(string(a.ID), string(b.ID))
	})
	return commands[0]
}

func originRank(origin plan.CommandOrigin) int {
	switch origin {
	case plan.CommandDeclared:
		return 0
	case plan.CommandInferred:
		return 1
	default:
		return 2
	}
}

func invocationOf(command plan.Command) (knowledge.Invocation, bool) {
	if command.Run == nil {
		return knowledge.Invocation{}, false
	}
	statements := knowledge.ParseStatements(*command.Run)
	for i := len(statements) - 1; i >= 0; i-- {
		if statements[i].Invocation.Executable == "" {
			continue
		}
		_, canonical := knowledge.StripDirectoryFlags(statements[i].Invocation)
		return canonical, true
	}
	return knowledge.Invocation{}, false
}

func hasArgsPrefix(prefix, args []string) bool {
	if len(prefix) > len(args) {
		return false
	}
	for i, part := range prefix {
		if args[i] != part {
			return false
		}
	}
	return true
}

func packageManagerConflict(existing, observed plan.Command) plan.Conflict {
	id := existing.ID
	return plan.Conflict{
		Subject:   commandSubject(existing),
		CommandID: &id,
		Message:   "CI invokes this task with a different package manager than the repository declaration.",
		Assertions: []plan.Candidate{
			{Value: deref(existing.Run), Evidence: existing.Evidence},
			{Value: deref(observed.Run), Evidence: observed.Evidence},
		},
	}
}

func commandSubject(command plan.Command) string {
	inv, ok := invocationOf(command)
	if ok {
		classified, isPM := knowledge.ClassifyManager(inv)
		if isPM && classified.Script != "" {
			return "command." + subjectToken(classified.Script) + ".run"
		}
		if isPM && classified.Install {
			return "dependencies.install"
		}
	}
	return "command." + subjectToken(command.Name) + ".run"
}

func subjectToken(name string) string {
	var b strings.Builder
	lastHyphen := true
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastHyphen = false
		case r == '.' || r == '-':
			if !lastHyphen && b.Len() > 0 {
				b.WriteRune(r)
				lastHyphen = true
			}
		default:
			if !lastHyphen && b.Len() > 0 {
				b.WriteRune('-')
				lastHyphen = true
			}
		}
	}
	token := strings.Trim(b.String(), ".-")
	if token == "" {
		return "command-unnamed"
	}
	return token
}
