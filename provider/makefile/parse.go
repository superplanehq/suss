package makefile

import (
	"strings"
	"unicode"

	"github.com/superplanehq/suss/knowledge"
)

type makefile struct {
	targets     []makeTarget
	limitations []string
	usesDocker  bool
	limitSeen   map[string]struct{}
	vars        map[string]string
	rawVars     map[string]string
}

type makeTarget struct {
	Name   string
	Recipe string
}

type lineKind int

const (
	lineOther lineKind = iota
	lineAssignment
	lineRule
)

func parseMakefile(contents string) makefile {
	parsed := makefile{
		limitSeen: make(map[string]struct{}),
		vars:      make(map[string]string),
		rawVars:   make(map[string]string),
	}
	lines := joinContinuations(strings.Split(contents, "\n"))

	var current []int
	inDefine := false
	for _, line := range lines {
		if inDefine {
			if directiveName(strings.TrimSpace(line)) == "endef" {
				inDefine = false
			}
			continue
		}

		if strings.HasPrefix(line, "\t") {
			if len(current) == 0 {
				continue
			}
			recipe := strings.TrimPrefix(line, "\t")
			recipe = stripRecipePrefix(recipe)
			for _, index := range current {
				parsed.targets[index].Recipe = appendRecipe(parsed.targets[index].Recipe, recipe)
			}
			continue
		}

		current = nil
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if strings.HasPrefix(trimmed, "$") {
			parsed.addLimitation("variable-expansion")
			continue
		}

		name := directiveName(trimmed)
		switch name {
		case "define":
			inDefine = true
			parsed.addLimitation("define")
			continue
		case "include", "-include", "sinclude":
			parsed.addLimitation("include")
			continue
		case "ifeq", "ifneq", "ifdef", "ifndef", "else", "endif":
			parsed.addLimitation("conditionals")
			continue
		case "export", "unexport", "override", "undefine":
			if kind, key, value, op := parseAssignment(stripExportOverride(trimmed)); kind == lineAssignment {
				parsed.applyAssignment(key, value, op)
			}
			continue
		}

		switch kind, key, value, op := parseAssignment(trimmed); kind {
		case lineAssignment:
			parsed.applyAssignment(key, value, op)
		case lineRule:
			current = parsed.addTargets(key)
			if inline := inlineRecipe(key); inline != "" {
				for _, index := range current {
					parsed.targets[index].Recipe = appendRecipe(parsed.targets[index].Recipe, inline)
				}
			}
		}
	}

	parsed.expandTargets()
	return parsed
}

func (m *makefile) addTargets(header string) []int {
	namePart, _, _ := strings.Cut(header, ":")
	namePart = strings.TrimSpace(namePart)
	namePart = strings.TrimSuffix(namePart, ":")
	var indexes []int
	for _, name := range strings.Fields(namePart) {
		if name == "" {
			continue
		}
		indexes = append(indexes, m.upsertTarget(name))
	}
	return indexes
}

func (m *makefile) upsertTarget(name string) int {
	for i, target := range m.targets {
		if target.Name == name {
			return i
		}
	}
	m.targets = append(m.targets, makeTarget{Name: name})
	return len(m.targets) - 1
}

func (m *makefile) applyAssignment(key, value, op string) {
	key = strings.TrimSpace(key)
	if key == "" || strings.ContainsAny(key, " \t:") {
		return
	}
	switch op {
	case ":=":
		m.vars[key] = m.expand(value)
		delete(m.rawVars, key)
	case "?=":
		if _, ok := m.vars[key]; ok {
			return
		}
		if _, ok := m.rawVars[key]; ok {
			return
		}
		m.rawVars[key] = value
	case "+=":
		if current, ok := m.vars[key]; ok {
			m.vars[key] = strings.TrimSpace(current + " " + m.expand(value))
			return
		}
		if current, ok := m.rawVars[key]; ok {
			m.rawVars[key] = strings.TrimSpace(current + " " + value)
			return
		}
		m.rawVars[key] = value
	default:
		m.rawVars[key] = value
		delete(m.vars, key)
	}
}

func (m *makefile) expandTargets() {
	var kept []makeTarget
	for _, target := range m.targets {
		name := strings.TrimSpace(m.expand(target.Name))
		recipe := m.expand(target.Recipe)
		if usesDocker(recipe) {
			m.usesDocker = true
		}
		if skipTarget(name) {
			continue
		}
		kept = append(kept, makeTarget{Name: name, Recipe: recipe})
	}
	m.targets = kept
}

func skipTarget(name string) bool {
	if name == "" || strings.Contains(name, "%") || strings.ContainsAny(name, "$'\"") {
		return true
	}
	if _, ok := specialTargets[name]; ok {
		return true
	}
	return !plausibleTarget(name)
}

func plausibleTarget(name string) bool {
	if name == "," || name == ";" || strings.HasPrefix(name, "-") {
		return false
	}
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("_-./+", r) {
			continue
		}
		return false
	}
	return true
}

func (m *makefile) addLimitation(value string) {
	if _, ok := m.limitSeen[value]; ok {
		return
	}
	m.limitSeen[value] = struct{}{}
	m.limitations = append(m.limitations, value)
}

func (m *makefile) expand(input string) string {
	return m.expandDepth(input, 0)
}

func (m *makefile) expandDepth(input string, depth int) string {
	if depth > 16 {
		m.addLimitation("variable-expansion")
		return input
	}

	var b strings.Builder
	for i := 0; i < len(input); {
		if input[i] != '$' {
			b.WriteByte(input[i])
			i++
			continue
		}
		if i+1 >= len(input) {
			b.WriteByte('$')
			break
		}
		switch input[i+1] {
		case '$':
			b.WriteByte('$')
			i += 2
		case '(':
			name, end, ok := readRef(input, i+2, '(', ')')
			if !ok {
				b.WriteString(input[i:])
				return b.String()
			}
			b.WriteString(m.expandRef(name, depth))
			i = end
		case '{':
			name, end, ok := readRef(input, i+2, '{', '}')
			if !ok {
				b.WriteString(input[i:])
				return b.String()
			}
			b.WriteString(m.expandRef(name, depth))
			i = end
		default:
			b.WriteString(m.expandRef(string(input[i+1]), depth))
			i += 2
		}
	}
	return b.String()
}

func (m *makefile) expandRef(name string, depth int) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if !simpleVarName(name) {
		m.addLimitation("variable-expansion")
		return "$(" + name + ")"
	}
	if _, ok := predefinedVars[name]; ok {
		m.addLimitation("variable-expansion")
		return "$(" + name + ")"
	}
	if value, ok := m.vars[name]; ok {
		return m.expandDepth(value, depth+1)
	}
	if value, ok := m.rawVars[name]; ok {
		return m.expandDepth(value, depth+1)
	}
	m.addLimitation("variable-expansion")
	if len(name) == 1 {
		return "$" + name
	}
	return "$(" + name + ")"
}

func inlineRecipe(header string) string {
	_, rest, found := strings.Cut(header, ":")
	if !found {
		return ""
	}
	rest = strings.TrimPrefix(rest, ":")
	_, recipe, found := strings.Cut(rest, ";")
	if !found {
		return ""
	}
	return stripRecipePrefix(strings.TrimSpace(recipe))
}

func readRef(input string, start int, open, closer byte) (string, int, bool) {
	depth := 1
	for i := start; i < len(input); i++ {
		switch input[i] {
		case open:
			depth++
		case closer:
			depth--
			if depth == 0 {
				return input[start:i], i + 1, true
			}
		}
	}
	return "", 0, false
}

func simpleVarName(name string) bool {
	if name == "" {
		return false
	}
	if len(name) == 1 && strings.ContainsRune("@<^.*?+|%", rune(name[0])) {
		return false
	}
	for i, r := range name {
		if r == '_' || unicode.IsLetter(r) {
			continue
		}
		if unicode.IsDigit(r) && i > 0 {
			continue
		}
		return false
	}
	return true
}

func parseAssignment(line string) (lineKind, string, string, string) {
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case ':':
			if i+1 < len(line) && line[i+1] == '=' {
				return lineAssignment, line[:i], strings.TrimSpace(line[i+2:]), ":="
			}
			if i+2 < len(line) && line[i:i+3] == "::=" {
				return lineAssignment, line[:i], strings.TrimSpace(line[i+3:]), ":="
			}
			return lineRule, line, "", ""
		case '?', '+', '!':
			if i+1 < len(line) && line[i+1] == '=' {
				return lineAssignment, line[:i], strings.TrimSpace(line[i+2:]), string(line[i]) + "="
			}
		case '=':
			return lineAssignment, line[:i], strings.TrimSpace(line[i+1:]), "="
		}
	}
	return lineOther, "", "", ""
}

func stripExportOverride(line string) string {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return line
	}
	switch fields[0] {
	case "export", "override", "unexport", "undefine":
		return strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
	default:
		return line
	}
}

func directiveName(line string) string {
	if line == "" {
		return ""
	}
	end := 0
	for end < len(line) && (unicode.IsLetter(rune(line[end])) || line[end] == '-') {
		end++
	}
	return line[:end]
}

func stripRecipePrefix(recipe string) string {
	for recipe != "" && (recipe[0] == '@' || recipe[0] == '-' || recipe[0] == '+') {
		recipe = recipe[1:]
	}
	return recipe
}

func appendRecipe(existing, next string) string {
	next = strings.TrimRight(next, " \t")
	if next == "" {
		return existing
	}
	if existing == "" {
		return next
	}
	return existing + "\n" + next
}

func joinContinuations(lines []string) []string {
	var joined []string
	var current strings.Builder
	for _, line := range lines {
		trimmedRight := strings.TrimRight(line, " \t")
		if strings.HasSuffix(trimmedRight, "\\") {
			current.WriteString(strings.TrimSuffix(trimmedRight, "\\"))
			current.WriteByte(' ')
			continue
		}
		current.WriteString(line)
		joined = append(joined, current.String())
		current.Reset()
	}
	if current.Len() > 0 {
		joined = append(joined, current.String())
	}
	return joined
}

func usesDocker(recipe string) bool {
	for _, inv := range knowledge.ParseScript(recipe) {
		if inv.Executable == "docker" || inv.Executable == "docker-compose" {
			return true
		}
	}
	return false
}

var specialTargets = map[string]struct{}{
	".PHONY": {}, ".SUFFIXES": {}, ".DEFAULT": {}, ".PRECIOUS": {},
	".INTERMEDIATE": {}, ".SECONDARY": {}, ".SECONDEXPANSION": {},
	".DELETE_ON_ERROR": {}, ".IGNORE": {}, ".LOW_RESOLUTION_TIME": {},
	".SILENT": {}, ".EXPORT_ALL_VARIABLES": {}, ".NOTPARALLEL": {},
	".ONESHELL": {}, ".POSIX": {}, ".NOEXPORT": {}, ".MAKE": {},
	".WAIT": {}, ".NOTINTERMEDIATE": {}, ".EXTRA_PREREQS": {},
	".LIBPATTERNS": {}, ".DEFAULT_GOAL": {},
}

var predefinedVars = map[string]struct{}{
	"MAKEFILE_LIST": {}, "MAKEFLAGS": {}, "MAKECMDGOALS": {},
	"CURDIR": {}, "MAKE": {}, "MAKELEVEL": {}, "MAKEFILES": {},
}
