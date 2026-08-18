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

// Statement is one shell list element, with env prefixes and `cd` captured
// rather than discarded. Variable expansion and subshells are not evaluated.
type Statement struct {
	Raw        string
	EnvNames   []string
	Chdir      string
	Invocation Invocation
}

// ParseScript splits a script on common list operators and extracts
// invocations. It does not expand variables or evaluate subshells.
func ParseScript(script string) []Invocation {
	var invocations []Invocation
	for _, stmt := range ParseStatements(script) {
		if stmt.Invocation.Executable == "" {
			continue
		}
		invocations = append(invocations, stmt.Invocation)
	}
	return invocations
}

// ParseStatements splits a script on common list operators and keeps each
// element's raw text, leading environment-variable names, and `cd` targets.
func ParseStatements(script string) []Statement {
	return parseStatements(script, true)
}

// ParseStatementsKeepPipelines is ParseStatements but treats `|` as part of
// one command rather than a list operator. GitHub Actions run blocks should
// preserve pipelines such as `curl | bash` as a single observed invocation.
func ParseStatementsKeepPipelines(script string) []Statement {
	return parseStatements(script, false)
}

func parseStatements(script string, splitPipes bool) []Statement {
	parts := splitCommandList(script, splitPipes)
	statements := make([]Statement, 0, len(parts))
	for _, part := range parts {
		if HasUnclosedGHAExpression(part) {
			// An unclosed ${{ ... }} cannot be tokenized without fabricating
			// text. Leave it uninterpreted.
			continue
		}
		statements = append(statements, parseStatement(part))
	}
	return statements
}

func parseStatement(part string) Statement {
	stmt := Statement{Raw: part}
	tokens := splitShell(part)
	names, rest := takeLeadingAssignments(tokens)
	stmt.EnvNames = names
	if len(rest) >= 2 && rest[0] == "cd" {
		stmt.Chdir = rest[1]
		return stmt
	}
	if len(rest) == 1 && rest[0] == "cd" {
		return stmt
	}
	inv, ok := parseInvocation(part)
	if ok {
		stmt.Invocation = inv
	}
	return stmt
}

func splitCommandList(script string, splitPipes bool) []string {
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
	inExpr := 0
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if !inSingle && !inDouble && r == '$' && i+2 < len(runes) && runes[i+1] == '{' && runes[i+2] == '{' {
			inExpr++
			current.WriteString("${{")
			i += 2
			continue
		}
		if !inSingle && !inDouble && inExpr > 0 && r == '}' && i+1 < len(runes) && runes[i+1] == '}' {
			current.WriteString("}}")
			i++
			inExpr--
			continue
		}
		if inExpr > 0 {
			current.WriteRune(r)
			continue
		}
		switch {
		case r == '\'' && !inDouble:
			inSingle = !inSingle
			current.WriteRune(r)
		case r == '"' && !inSingle:
			inDouble = !inDouble
			current.WriteRune(r)
		case startsComment(inSingle, inDouble, current.String()) && r == '#':
			i = skipToNewline(runes, i)
		case !inSingle && !inDouble && (r == ';' || r == '\n'):
			flush()
		case !inSingle && !inDouble && r == '&' && i+1 < len(runes) && runes[i+1] == '&':
			flush()
			i++
		case !inSingle && !inDouble && r == '|' && i+1 < len(runes) && runes[i+1] == '|':
			flush()
			i++
		case splitPipes && !inSingle && !inDouble && r == '|':
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

	runes := []rune(part)
	inExpr := 0
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if !inSingle && !inDouble && r == '$' && i+2 < len(runes) && runes[i+1] == '{' && runes[i+2] == '{' {
			inExpr++
			current.WriteString("${{")
			i += 2
			continue
		}
		if !inSingle && !inDouble && inExpr > 0 && r == '}' && i+1 < len(runes) && runes[i+1] == '}' {
			current.WriteString("}}")
			i++
			inExpr--
			continue
		}
		if inExpr > 0 {
			current.WriteRune(r)
			continue
		}
		switch {
		case r == '\'' && !inDouble:
			inSingle = !inSingle
		case r == '"' && !inSingle:
			inDouble = !inDouble
		case startsComment(inSingle, inDouble, current.String()) && r == '#':
			flush()
			return tokens
		case unicode.IsSpace(r) && !inSingle && !inDouble:
			flush()
		default:
			current.WriteRune(r)
		}
	}
	flush()
	return tokens
}

func startsComment(inSingle, inDouble bool, current string) bool {
	if inSingle || inDouble {
		return false
	}
	if current == "" {
		return true
	}
	return unicode.IsSpace(rune(current[len(current)-1]))
}

func skipToNewline(runes []rune, i int) int {
	for i+1 < len(runes) && runes[i+1] != '\n' {
		i++
	}
	return i
}

func dropLeadingAssignments(tokens []string) []string {
	_, rest := takeLeadingAssignments(tokens)
	return rest
}

// RedactAssignmentValues replaces values in leading NAME=value prefixes so
// command text never carries secret or literal assignment values.
func RedactAssignmentValues(raw string) string {
	rest := strings.TrimLeft(raw, " \t")
	var parts []string
	for {
		name, after, ok := cutLeadingAssignment(rest)
		if !ok {
			if rest != "" {
				parts = append(parts, rest)
			}
			return strings.Join(parts, " ")
		}
		parts = append(parts, name+"=$"+name)
		rest = strings.TrimLeft(after, " \t")
	}
}

func cutLeadingAssignment(s string) (name, rest string, ok bool) {
	i := 0
	for i < len(s) && (s[i] == '_' || (s[i] >= 'A' && s[i] <= 'Z') || (s[i] >= 'a' && s[i] <= 'z') || (s[i] >= '0' && s[i] <= '9' && i > 0)) {
		i++
	}
	if i == 0 || i >= len(s) || s[i] != '=' {
		return "", s, false
	}
	name = s[:i]
	i++
	if i < len(s) && (s[i] == '\'' || s[i] == '"') {
		q := s[i]
		i++
		for i < len(s) && s[i] != q {
			i++
		}
		if i < len(s) {
			i++
		}
		return name, s[i:], true
	}
	if end := ghaExprClose(s, i); end >= 0 {
		return name, s[end:], true
	}
	if strings.HasPrefix(s[i:], "${{") {
		return "", s, false
	}
	for i < len(s) && s[i] != ' ' && s[i] != '\t' {
		if end := ghaExprClose(s, i); end >= 0 {
			i = end
			continue
		}
		if strings.HasPrefix(s[i:], "${{") {
			return "", s, false
		}
		i++
	}
	return name, s[i:], true
}

// HasUnclosedGHAExpression reports whether s contains a `${{` that is not
// closed by `}}`. Those cannot be tokenized without fabricating text.
func HasUnclosedGHAExpression(s string) bool {
	for i := 0; i < len(s); {
		j := strings.Index(s[i:], "${{")
		if j < 0 {
			return false
		}
		i += j
		end := ghaExprClose(s, i)
		if end < 0 {
			return true
		}
		i = end
	}
	return false
}

func ghaExprClose(s string, i int) int {
	if i < 0 || i+3 > len(s) || s[i:i+3] != "${{" {
		return -1
	}
	end := strings.Index(s[i+3:], "}}")
	if end < 0 {
		return -1
	}
	return i + 3 + end + 2
}

func takeLeadingAssignments(tokens []string) ([]string, []string) {
	i := 0
	var names []string
	for i < len(tokens) {
		name, _, ok := strings.Cut(tokens[i], "=")
		if !ok || name == "" || strings.ContainsAny(name, "/\\") {
			break
		}
		names = append(names, name)
		i++
	}
	return names, tokens[i:]
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
