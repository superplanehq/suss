// Package knowledge is the declarative mapping from well-known tool
// invocations to capabilities. Entries are data, not code.
//
// render.toolCapabilities duplicates capability facts from this file for
// "configured but no matching command" copy. Keep the two in sync until both
// are derived from invocations.json.
package knowledge

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"sync"
	"unicode"

	"github.com/superplanehq/suss/plan"
)

//go:embed invocations.json
var invocationsJSON []byte

// Invocation is one parsed command from a script body or observed run line.
type Invocation struct {
	Executable string
	Args       []string
}

// Match is one knowledge-base hit for an invocation.
type Match struct {
	ID          string
	Capability  plan.Capability
	Confidence  plan.Confidence
	Description string
}

type rule struct {
	ID           string            `json:"id"`
	Executable   string            `json:"executable"`
	ArgsPrefix   []string          `json:"argsPrefix"`
	Capabilities []plan.Capability `json:"capabilities"`
	Confidence   plan.Confidence   `json:"confidence"`
	Description  string            `json:"description"`
}

type file struct {
	Invocations []rule `json:"invocations"`
}

var (
	loadOnce sync.Once
	rules    []rule
	loadErr  error
)

func loaded() ([]rule, error) {
	loadOnce.Do(func() {
		var parsed file
		decoder := json.NewDecoder(bytes.NewReader(invocationsJSON))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&parsed); err != nil {
			loadErr = fmt.Errorf("parse knowledge base: %w", err)
			return
		}
		if len(parsed.Invocations) == 0 {
			loadErr = fmt.Errorf("knowledge base has no invocations")
			return
		}
		rules = parsed.Invocations
	})
	return rules, loadErr
}

// Interpret returns knowledge-base matches for a single invocation. When
// several rules match, only the most specific args-prefix group is kept.
func Interpret(inv Invocation) []Match {
	loadedRules, err := loaded()
	if err != nil {
		return nil
	}

	executable := canonicalizeExecutable(inv.Executable)
	if executable == "" {
		return nil
	}

	bestLen := -1
	var matches []Match
	for _, rule := range loadedRules {
		if rule.Executable != executable {
			continue
		}
		if !hasArgsPrefix(inv.Args, rule.ArgsPrefix) {
			continue
		}
		prefixLen := len(rule.ArgsPrefix)
		if prefixLen < bestLen {
			continue
		}
		if prefixLen > bestLen {
			bestLen = prefixLen
			matches = matches[:0]
		}
		for _, capability := range rule.Capabilities {
			matches = append(matches, Match{
				ID:          rule.ID,
				Capability:  capability,
				Confidence:  rule.Confidence,
				Description: rule.Description,
			})
		}
	}
	return uniqueCapabilities(matches)
}

// InterpretScript parses a shell-ish script and interprets each command.
func InterpretScript(script string) []Match {
	var matches []Match
	for _, inv := range ParseScript(script) {
		matches = append(matches, Interpret(inv)...)
	}
	return uniqueCapabilities(matches)
}

// ParseScript splits a script on common list operators and extracts
// invocations. It does not expand variables or evaluate subshells.
func ParseScript(script string) []Invocation {
	var invocations []Invocation
	for _, part := range splitCommandList(script) {
		inv, ok := parseInvocation(part)
		if ok {
			invocations = append(invocations, inv)
		}
	}
	return invocations
}

func splitCommandList(script string) []string {
	var parts []string
	var current strings.Builder
	inSingle, inDouble := false, false

	flush := func() {
		part := strings.TrimSpace(current.String())
		current.Reset()
		if part != "" {
			parts = append(parts, part)
		}
	}

	runes := []rune(script)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r == '\'' && !inDouble:
			inSingle = !inSingle
			current.WriteRune(r)
		case r == '"' && !inSingle:
			inDouble = !inDouble
			current.WriteRune(r)
		case !inSingle && !inDouble && (r == ';' || r == '\n'):
			flush()
		case !inSingle && !inDouble && r == '&' && i+1 < len(runes) && runes[i+1] == '&':
			flush()
			i++
		case !inSingle && !inDouble && r == '|' && i+1 < len(runes) && runes[i+1] == '|':
			flush()
			i++
		case !inSingle && !inDouble && r == '|':
			flush()
		default:
			current.WriteRune(r)
		}
	}
	flush()
	return parts
}

func parseInvocation(part string) (Invocation, bool) {
	tokens := splitShell(part)
	tokens = dropLeadingAssignments(tokens)
	tokens = dropWrappers(tokens)
	if len(tokens) == 0 {
		return Invocation{}, false
	}

	executable := canonicalizeExecutable(tokens[0])
	if executable == "" {
		return Invocation{}, false
	}
	return Invocation{Executable: executable, Args: tokens[1:]}, true
}

func splitShell(part string) []string {
	var tokens []string
	var current strings.Builder
	inSingle, inDouble := false, false

	flush := func() {
		if current.Len() == 0 {
			return
		}
		tokens = append(tokens, current.String())
		current.Reset()
	}

	for _, r := range part {
		switch {
		case r == '\'' && !inDouble:
			inSingle = !inSingle
		case r == '"' && !inSingle:
			inDouble = !inDouble
		case unicode.IsSpace(r) && !inSingle && !inDouble:
			flush()
		default:
			current.WriteRune(r)
		}
	}
	flush()
	return tokens
}

func dropLeadingAssignments(tokens []string) []string {
	i := 0
	for i < len(tokens) {
		name, _, ok := strings.Cut(tokens[i], "=")
		if !ok || name == "" || strings.ContainsAny(name, "/\\") {
			break
		}
		i++
	}
	return tokens[i:]
}

func dropWrappers(tokens []string) []string {
	if len(tokens) == 0 {
		return tokens
	}

	switch tokens[0] {
	case "npx", "pnpx", "bunx", "c8", "nyc":
		return dropLeadingFlags(tokens[1:])
	case "npm", "pnpm", "yarn":
		if len(tokens) >= 2 && tokens[1] == "exec" {
			return dropLeadingFlags(tokens[2:])
		}
	case "bun":
		if len(tokens) >= 2 && tokens[1] == "x" {
			return dropLeadingFlags(tokens[2:])
		}
	}
	return tokens
}

func dropLeadingFlags(tokens []string) []string {
	i := 0
	for i < len(tokens) && strings.HasPrefix(tokens[i], "-") {
		i++
	}
	return tokens[i:]
}

func canonicalizeExecutable(executable string) string {
	executable = strings.TrimSpace(executable)
	executable = strings.Trim(executable, `"'`)
	if executable == "" {
		return ""
	}
	executable = strings.ReplaceAll(executable, "\\", "/")
	base := path.Base(executable)
	base = strings.TrimSuffix(base, ".cmd")
	base = strings.TrimSuffix(base, ".exe")
	return base
}

func hasArgsPrefix(args, prefix []string) bool {
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

func uniqueCapabilities(matches []Match) []Match {
	if len(matches) < 2 {
		return matches
	}
	seen := make(map[plan.Capability]struct{}, len(matches))
	out := make([]Match, 0, len(matches))
	for _, match := range matches {
		if _, ok := seen[match.Capability]; ok {
			continue
		}
		seen[match.Capability] = struct{}{}
		out = append(out, match)
	}
	return out
}
