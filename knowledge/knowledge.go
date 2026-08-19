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
	Directory  string
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
	ArgsContains []string          `json:"argsContains"`
	ArgsExact    bool              `json:"argsExact"`
	Capabilities []plan.Capability `json:"capabilities"`
	Confidence   plan.Confidence   `json:"confidence"`
	Description  string            `json:"description"`
}

type file struct {
	Invocations []rule         `json:"invocations"`
	TaskNames   []taskNameRule `json:"taskNames"`
}

type taskNameRule struct {
	ID           string            `json:"id"`
	Names        []string          `json:"names"`
	Prefixes     []string          `json:"prefixes"`
	Capabilities []plan.Capability `json:"capabilities"`
	Confidence   plan.Confidence   `json:"confidence"`
	Description  string            `json:"description"`
}

var (
	loadOnce  sync.Once
	rules     []rule
	taskRules []taskNameRule
	loadErr   error
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
		taskRules = parsed.TaskNames
	})
	return rules, loadErr
}

// InterpretTaskName maps an explicitly declared task name to a lifecycle
// capability. The boolean distinguishes unknown names from known task classes
// such as generation and cleanup that intentionally have no v1 capability.
func InterpretTaskName(name string) ([]Match, bool) {
	if _, err := loaded(); err != nil {
		return nil, false
	}
	name = strings.ToLower(strings.TrimSpace(name))
	for _, rule := range taskRules {
		if !matchesTaskName(name, rule.Names) && !matchesTaskPrefix(name, rule.Prefixes) {
			continue
		}
		matches := make([]Match, 0, len(rule.Capabilities))
		for _, capability := range rule.Capabilities {
			matches = append(matches, Match{
				ID:          rule.ID,
				Capability:  capability,
				Confidence:  rule.Confidence,
				Description: rule.Description,
			})
		}
		return matches, true
	}
	return nil, false
}

func matchesTaskName(name string, candidates []string) bool {
	for _, candidate := range candidates {
		if name == candidate {
			return true
		}
	}
	return false
}

func matchesTaskPrefix(name string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(name, prefix) && len(name) > len(prefix) && strings.ContainsRune("-:._/", rune(name[len(prefix)])) {
			return true
		}
	}
	return false
}

// Interpret returns knowledge-base matches for a single invocation. When
// several rules match, only the most specific args-prefix and args-contains
// group is kept.
func Interpret(inv Invocation) []Match {
	loadedRules, err := loaded()
	if err != nil {
		return nil
	}

	executable := canonicalizeExecutable(inv.Executable)
	if executable == "" {
		return nil
	}
	if executable == "cargo" {
		inv.Args = normalizeCargoArgs(inv.Args)
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
		if !hasArgsContains(inv.Args, rule.ArgsContains) {
			continue
		}
		if rule.ArgsExact && len(inv.Args) != len(rule.ArgsPrefix) {
			continue
		}
		specificity := len(rule.ArgsPrefix) + len(rule.ArgsContains)
		if specificity < bestLen {
			continue
		}
		if specificity > bestLen {
			bestLen = specificity
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

// CommandName is the stable display name for an observed invocation.
// Package-manager installs and scripts keep their existing names. Tools
// that do not take subcommands keep the executable so filter values are
// not treated as commands.
func CommandName(inv Invocation) string {
	classified, ok := ClassifyManager(inv)
	if ok && classified.Install {
		return "install dependencies"
	}
	if ok && classified.Script != "" {
		return classified.Script
	}
	if inv.Executable == "" {
		return "command"
	}
	if !takesSubcommand(inv.Executable) {
		return inv.Executable
	}
	for _, arg := range inv.Args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return inv.Executable + " " + arg
	}
	return inv.Executable
}

func takesSubcommand(executable string) bool {
	switch canonicalizeExecutable(executable) {
	case "phpunit", "simple-phpunit", "pest", "phpstan", "psalm", "phpcs", "pint", "php-cs-fixer":
		return false
	default:
		return true
	}
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
	WorkingDir string
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
	if dir := workingDirectoryFlag(rest); dir != "" {
		stmt.WorkingDir = dir
	}
	inv, ok := parseInvocation(part)
	if ok {
		stmt.Invocation = inv
	}
	return stmt
}

func workingDirectoryFlag(tokens []string) string {
	if len(tokens) == 0 {
		return ""
	}
	dir, _ := StripDirectoryFlags(Invocation{
		Executable: canonicalizeExecutable(tokens[0]),
		Args:       tokens[1:],
	})
	return dir
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
	tokens, dir := dropWrappers(tokens)
	tokens = stripCargoToolchain(tokens)
	if len(tokens) == 0 {
		return Invocation{}, false
	}

	executable := canonicalizeExecutable(tokens[0])
	if executable == "" {
		return Invocation{}, false
	}
	return Invocation{Executable: executable, Args: tokens[1:], Directory: dir}, true
}

func splitShell(part string) []string {
	return splitShellTokens(part, false)
}

func splitShellQuoted(part string) []string {
	return splitShellTokens(part, true)
}

func splitShellTokens(part string, keepQuotes bool) []string {
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
			if keepQuotes {
				current.WriteRune(r)
			}
		case r == '"' && !inSingle:
			inDouble = !inDouble
			if keepQuotes {
				current.WriteRune(r)
			}
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

func dropWrappers(tokens []string) ([]string, string) {
	dir := ""
	for range 8 {
		next, nextDir := unwrapOnce(tokens)
		if nextDir != "" {
			dir = nextDir
		}
		if sameTokens(next, tokens) {
			return next, dir
		}
		tokens = next
	}
	return tokens, dir
}

func unwrapOnce(tokens []string) ([]string, string) {
	if len(tokens) == 0 {
		return tokens, ""
	}

	switch tokens[0] {
	case "npx", "pnpx", "bunx", "c8", "nyc":
		return dropLeadingFlags(tokens[1:]), ""
	case "bundle":
		if len(tokens) >= 2 && tokens[1] == "exec" {
			return dropLeadingFlags(tokens[2:]), ""
		}
	case "composer":
		rest := skipComposerGlobalOptions(tokens[1:])
		if len(rest) == 0 {
			break
		}
		if rest[0] == "exec" {
			unwrapped := skipComposerGlobalOptions(rest[1:])
			if len(unwrapped) > 0 && unwrapped[0] == "--" {
				return unwrapped[1:], ""
			}
			return unwrapped, ""
		}
		return append([]string{"composer"}, rest...), ""
	case "php":
		rest := skipPHPCLIOptions(tokens[1:])
		if len(rest) == 0 {
			break
		}
		if isVendorBinPath(rest[0]) {
			return rest, ""
		}
		return append([]string{"php"}, rest...), ""
	case "poetry", "pipenv", "pdm":
		rest, dir := skipPythonManagerGlobals(tokens[1:])
		if len(rest) >= 1 && rest[0] == "run" {
			return dropLeadingFlags(rest[1:]), dir
		}
	case "uv":
		rest, dir := skipUVGlobals(tokens[1:])
		if len(rest) >= 1 && rest[0] == "run" {
			inner, innerDir := takeUVDirectory(rest[1:])
			if innerDir != "" {
				dir = innerDir
			}
			return dropLeadingFlagsWithValues(inner, uvRunValueFlags), dir
		}
		if len(rest) >= 2 && rest[0] == "tool" && rest[1] == "run" {
			inner, innerDir := takeUVDirectory(rest[2:])
			if innerDir != "" {
				dir = innerDir
			}
			return dropLeadingFlagsWithValues(inner, uvRunValueFlags), dir
		}
		return tokens, dir
	case "python", "python3":
		if len(tokens) >= 3 && tokens[1] == "-m" {
			return dropLeadingFlags(tokens[2:]), ""
		}
		if len(tokens) >= 2 && path.Base(tokens[1]) == "manage.py" {
			return append([]string{tokens[0], "manage.py"}, tokens[2:]...), ""
		}
	case "npm", "pnpm", "yarn":
		if len(tokens) >= 2 && tokens[1] == "exec" {
			return dropLeadingFlags(tokens[2:]), ""
		}
	case "bun":
		if len(tokens) >= 2 && tokens[1] == "x" {
			return dropLeadingFlags(tokens[2:]), ""
		}
	case "rustup":
		if unwrapped := unwrapRustupRun(tokens); len(unwrapped) > 0 {
			return dropWrappers(unwrapped)
		}
	}
	return tokens, ""
}

var uvDirectoryFlags = map[string]struct{}{
	"--directory": {}, "-C": {}, "--project": {},
}

var uvGlobalValueFlags = map[string]struct{}{
	"--directory": {}, "-C": {}, "--project": {},
	"--config-file": {}, "--cache-dir": {},
	"--python": {}, "-p": {},
}

var pythonManagerValueFlags = map[string]struct{}{
	"--index-url": {}, "-i": {}, "--extra-index-url": {},
	"--trusted-host": {}, "--find-links": {}, "-f": {},
	"--directory": {}, "-C": {}, "--project": {}, "-p": {},
	"--python": {}, "--config-file": {}, "--group": {},
}

var pythonRunnerDirFlags = map[string]struct{}{
	"--directory": {}, "-C": {}, "--project": {}, "-p": {},
}

func skipPythonManagerGlobals(tokens []string) ([]string, string) {
	dir := ""
	i := 0
	for i < len(tokens) && strings.HasPrefix(tokens[i], "-") {
		name, value, hasValue := strings.Cut(tokens[i], "=")
		if name == "--" {
			return tokens[i+1:], dir
		}
		if _, isDir := pythonRunnerDirFlags[name]; isDir {
			if hasValue {
				dir = value
				i++
				continue
			}
			if i+1 < len(tokens) && !strings.HasPrefix(tokens[i+1], "-") {
				dir = tokens[i+1]
				i += 2
				continue
			}
			i++
			continue
		}
		if _, ok := pythonManagerValueFlags[name]; ok && !hasValue && i+1 < len(tokens) && !strings.HasPrefix(tokens[i+1], "-") {
			i += 2
			continue
		}
		i++
	}
	return tokens[i:], dir
}

func skipUVGlobals(tokens []string) ([]string, string) {
	dir := ""
	i := 0
	for i < len(tokens) && strings.HasPrefix(tokens[i], "-") {
		name, value, hasValue := strings.Cut(tokens[i], "=")
		if name == "--" {
			return tokens[i+1:], dir
		}
		if captured, next, ok := takeNamedDirectory(name, value, hasValue, tokens, i, uvDirectoryFlags); ok {
			if captured != "" {
				dir = captured
			}
			i = next
			continue
		}
		if _, ok := uvGlobalValueFlags[name]; ok && !hasValue && i+1 < len(tokens) && !strings.HasPrefix(tokens[i+1], "-") {
			i += 2
			continue
		}
		i++
	}
	return tokens[i:], dir
}

func takeUVDirectory(tokens []string) ([]string, string) {
	dir := ""
	out := make([]string, 0, len(tokens))
	i := 0
	for i < len(tokens) && strings.HasPrefix(tokens[i], "-") {
		name, value, hasValue := strings.Cut(tokens[i], "=")
		if name == "--" {
			return append(out, tokens[i:]...), dir
		}
		if captured, next, ok := takeNamedDirectory(name, value, hasValue, tokens, i, uvDirectoryFlags); ok {
			if captured != "" {
				dir = captured
			}
			i = next
			continue
		}
		out = append(out, tokens[i])
		if _, ok := uvRunValueFlags[name]; ok && !hasValue && i+1 < len(tokens) && !strings.HasPrefix(tokens[i+1], "-") {
			out = append(out, tokens[i+1])
			i += 2
			continue
		}
		i++
	}
	return append(out, tokens[i:]...), dir
}

func takeNamedDirectory(name, value string, hasValue bool, tokens []string, i int, flags map[string]struct{}) (string, int, bool) {
	if _, ok := flags[name]; !ok {
		return "", i, false
	}
	if hasValue {
		return value, i + 1, true
	}
	if i+1 < len(tokens) && !strings.HasPrefix(tokens[i+1], "-") {
		return tokens[i+1], i + 2, true
	}
	return "", i + 1, true
}

func isVendorBinPath(value string) bool {
	value = strings.ReplaceAll(value, "\\", "/")
	return strings.HasPrefix(value, "vendor/bin/") || strings.Contains(value, "/vendor/bin/")
}

// skipPHPCLIOptions drops php(1) flags and their values so a following
// script or vendor/bin executable can be unwrapped. -f/--file values are
// kept as the PHP script target. Other value-taking options follow `php --help`.
func skipPHPCLIOptions(tokens []string) []string {
	i := 0
	for i < len(tokens) {
		token := tokens[i]
		if token == "--" {
			return tokens[i+1:]
		}
		if !strings.HasPrefix(token, "-") {
			return tokens[i:]
		}
		name, hasValue := phpCLIOption(token)
		if phpCLIExecutionMode(name) {
			return nil
		}
		if name == "-f" || name == "--file" {
			target, rest, ok := phpFileOptionTarget(tokens, i, token, hasValue)
			if !ok {
				return nil
			}
			return append([]string{target}, rest...)
		}
		if phpCLIOptionTakesValue(name) && !hasValue {
			if i+1 >= len(tokens) {
				return nil
			}
			i += 2
			continue
		}
		i++
	}
	return nil
}

func phpFileOptionTarget(tokens []string, i int, token string, hasValue bool) (string, []string, bool) {
	if hasValue {
		var target string
		if strings.HasPrefix(token, "--") {
			_, target, _ = strings.Cut(token, "=")
		} else {
			target = strings.TrimPrefix(token[2:], "=")
		}
		if target == "" {
			return "", nil, false
		}
		return target, tokens[i+1:], true
	}
	if i+1 >= len(tokens) {
		return "", nil, false
	}
	return tokens[i+1], tokens[i+2:], true
}

func skipComposerGlobalOptions(args []string) []string {
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") && args[i] != "--" {
		name, _, hasValue := strings.Cut(args[i], "=")
		if !hasValue && composerGlobalOptionTakesValue(name) && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			i += 2
			continue
		}
		i++
	}
	return args[i:]
}

func composerGlobalOptionTakesValue(name string) bool {
	return name == "--working-dir" || name == "-d"
}

func phpCLIOption(token string) (name string, hasValue bool) {
	if strings.HasPrefix(token, "--") {
		name, _, hasValue = strings.Cut(token, "=")
		return name, hasValue
	}
	if len(token) > 2 {
		return token[:2], true
	}
	return token, false
}

func phpCLIExecutionMode(name string) bool {
	switch name {
	case "-r", "--run",
		"-l", "--syntax-check",
		"-s", "-w",
		"-B", "--process-begin",
		"-R", "--process-code",
		"-F", "--process-file",
		"-E", "--process-end",
		"-S", "--server":
		return true
	default:
		return false
	}
}

func phpCLIOptionTakesValue(name string) bool {
	switch name {
	case "-c", "--php-ini",
		"-d", "--define",
		"-f", "--file",
		"-r", "--run",
		"-B", "--process-begin",
		"-R", "--process-code",
		"-F", "--process-file",
		"-E", "--process-end",
		"-S", "--server",
		"-t", "--docroot",
		"-z", "--zend-extension":
		return true
	default:
		return false
	}
}

func unwrapRustupRun(tokens []string) []string {
	if len(tokens) == 0 || tokens[0] != "rustup" {
		return nil
	}
	rest := dropLeadingFlags(tokens[1:])
	if len(rest) < 3 || rest[0] != "run" {
		return nil
	}
	return rest[2:]
}

func stripCargoToolchain(tokens []string) []string {
	if len(tokens) < 2 || tokens[0] != "cargo" {
		return tokens
	}
	selector := tokens[1]
	if !strings.HasPrefix(selector, "+") || selector == "+" {
		return tokens
	}
	out := make([]string, 0, len(tokens)-1)
	out = append(out, "cargo")
	return append(out, tokens[2:]...)
}

func normalizeCargoArgs(args []string) []string {
	if len(args) > 0 && strings.HasPrefix(args[0], "+") && args[0] != "+" {
		args = args[1:]
	}
	return stripCargoGlobalOptions(args)
}

func stripCargoGlobalOptions(args []string) []string {
	i := 0
	for i < len(args) {
		arg := args[i]
		if arg == "--" || !strings.HasPrefix(arg, "-") {
			break
		}
		name, _, hasValue := strings.Cut(arg, "=")
		if option, attached := cargoGlobalOptionName(name); option != "" {
			if attached || hasValue {
				i++
				continue
			}
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i += 2
				continue
			}
			i++
			continue
		}
		if isCargoGlobalOption(name) {
			i++
			continue
		}
		break
	}
	return args[i:]
}

func cargoGlobalOptionName(name string) (option string, attached bool) {
	if cargoGlobalOptionTakesValue(name) {
		return name, false
	}
	for _, short := range []string{"-Z", "-C"} {
		if strings.HasPrefix(name, short) && len(name) > len(short) && !strings.HasPrefix(name, "--") {
			return short, true
		}
	}
	return "", false
}

func cargoGlobalOptionTakesValue(name string) bool {
	switch name {
	case "--color", "--config", "-Z", "-C", "--manifest-path", "--explain":
		return true
	default:
		return false
	}
}

func isCargoGlobalOption(name string) bool {
	switch name {
	case "--verbose", "--quiet", "-q", "--offline", "--locked", "--frozen":
		return true
	}
	return isCargoVerboseFlag(name)
}

func isCargoVerboseFlag(name string) bool {
	if name == "--verbose" {
		return true
	}
	if !strings.HasPrefix(name, "-") || strings.HasPrefix(name, "--") || len(name) < 2 {
		return false
	}
	for _, r := range name[1:] {
		if r != 'v' {
			return false
		}
	}
	return true
}

func sameTokens(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func dropLeadingFlags(tokens []string) []string {
	return dropLeadingFlagsWithValues(tokens, nil)
}

var uvRunValueFlags = map[string]struct{}{
	"--directory": {}, "-C": {}, "--project": {}, "--package": {},
	"--python": {}, "-p": {}, "--group": {}, "--no-group": {}, "--only-group": {},
	"--extra": {}, "--no-extra": {}, "--with": {}, "--with-editable": {},
	"--with-requirements": {}, "--env-file": {}, "--index": {}, "--default-index": {},
	"--config-file": {}, "--cache-dir": {}, "--keyring-provider": {},
}

func dropLeadingFlagsWithValues(tokens []string, valueFlags map[string]struct{}) []string {
	i := 0
	for i < len(tokens) && strings.HasPrefix(tokens[i], "-") {
		name, _, hasValue := strings.Cut(tokens[i], "=")
		if hasValue {
			i++
			continue
		}
		if _, ok := valueFlags[name]; ok && i+1 < len(tokens) && !strings.HasPrefix(tokens[i+1], "-") {
			i += 2
			continue
		}
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

func hasArgsContains(args, required []string) bool {
	if len(required) == 0 {
		return true
	}
	have := make(map[string]struct{}, len(args))
	for _, arg := range args {
		have[arg] = struct{}{}
	}
	for _, want := range required {
		if _, ok := have[want]; !ok {
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
