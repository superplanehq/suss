package python

import (
	"bufio"
	"strings"
	"unicode"
)

type pythonProject struct {
	Manifest              string
	RequiresPython        string
	RequiresPythonSource  string
	RequiresPythonPointer string
	Dependencies          map[string]depDeclaration
	ToolTables            map[string]struct{}
	ManagerTables         map[string]struct{}
	HasProjectTable       bool
	HasPackageTable       bool
}

type depDeclaration struct {
	Name   string
	Source string
}

type lockfile struct {
	Manager string
	File    string
}

type tomlSection struct {
	scalars map[string]string
	arrays  map[string][]string
	keys    []string
}

var knownFrameworks = []string{"django", "flask", "fastapi"}

var knownTools = []string{"pytest", "pytest-django", "ruff", "black", "mypy", "pyright", "flake8", "pylint", "isort", "tox", "nox"}

var managerTables = map[string]string{
	"poetry":              "poetry",
	"uv":                  "uv",
	"pdm":                 "pdm",
	"hatch":               "hatch",
	"poetry.dependencies": "poetry",
	"poetry.group":        "poetry",
}

func parsePyproject(contents string) pythonProject {
	doc := parseTOML(contents)
	parsed := pythonProject{
		Manifest:      "pyproject.toml",
		Dependencies:  make(map[string]depDeclaration),
		ToolTables:    make(map[string]struct{}),
		ManagerTables: make(map[string]struct{}),
	}

	if project, ok := doc["project"]; ok {
		parsed.HasProjectTable = true
		setRequiresPython(&parsed, project.scalars["requires-python"], "pyproject.toml", "/requires-python")
		addDependencies(&parsed, "pyproject.toml", "/project/dependencies", project.arrays["dependencies"])
	}
	for name, section := range doc {
		switch {
		case name == "project.optional-dependencies" || strings.HasPrefix(name, "project.optional-dependencies."):
			for _, values := range section.arrays {
				addDependencies(&parsed, "pyproject.toml", "/project/optional-dependencies", values)
			}
		case name == "dependency-groups" || strings.HasPrefix(name, "dependency-groups."):
			for _, values := range section.arrays {
				addDependencies(&parsed, "pyproject.toml", "/dependency-groups", values)
			}
		case name == "tool.pdm.dev-dependencies" || strings.HasPrefix(name, "tool.pdm.dev-dependencies."):
			for _, values := range section.arrays {
				addDependencies(&parsed, "pyproject.toml", "/tool/pdm/dev-dependencies", values)
			}
			recordToolTable(&parsed, strings.TrimPrefix(name, "tool."))
		case strings.HasPrefix(name, "tool."):
			recordToolTable(&parsed, strings.TrimPrefix(name, "tool."))
		}
	}

	if poetry, ok := doc["tool.poetry.dependencies"]; ok {
		setRequiresPython(&parsed, poetryScalar(poetry, "python"), "pyproject.toml", "/tool/poetry/dependencies/python")
		for _, name := range poetry.keys {
			if name != "python" {
				addDependency(&parsed, "pyproject.toml", "/tool/poetry/dependencies", name)
			}
		}
	}
	for name, section := range doc {
		if !strings.HasPrefix(name, "tool.poetry.group.") || !strings.HasSuffix(name, ".dependencies") {
			continue
		}
		for _, dep := range section.keys {
			if dep != "python" {
				addDependency(&parsed, "pyproject.toml", "/tool/poetry/group", dep)
			}
		}
	}
	return parsed
}

func parsePipfile(contents string) pythonProject {
	doc := parseTOML(contents)
	parsed := pythonProject{
		Manifest:      "Pipfile",
		Dependencies:  make(map[string]depDeclaration),
		ToolTables:    make(map[string]struct{}),
		ManagerTables: map[string]struct{}{"pipenv": {}},
	}
	if requires, ok := doc["requires"]; ok {
		if version := requires.scalars["python_version"]; version != "" {
			setRequiresPython(&parsed, version, "Pipfile", "/requires/python_version")
		} else {
			setRequiresPython(&parsed, requires.scalars["python_full_version"], "Pipfile", "/requires/python_full_version")
		}
	}
	for _, table := range []string{"packages", "dev-packages"} {
		section, ok := doc[table]
		if !ok {
			continue
		}
		for _, name := range section.keys {
			addDependency(&parsed, "Pipfile", "/"+table, name)
		}
	}
	return parsed
}

func parseSetupPy(contents string) pythonProject {
	parsed := pythonProject{
		Manifest:     "setup.py",
		Dependencies: make(map[string]depDeclaration),
		ToolTables:   make(map[string]struct{}),
		ManagerTables: map[string]struct{}{
			"pip": {},
		},
		HasPackageTable: true,
	}
	for _, match := range setupDependencyStrings(contents) {
		addKnownDependency(&parsed, "setup.py", "/setup", match)
	}
	return parsed
}

func parseRequirements(contents string) []string {
	var names []string
	scanner := bufio.NewScanner(strings.NewReader(contents))
	for scanner.Scan() {
		line, _, _ := strings.Cut(scanner.Text(), "#")
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "-") {
			continue
		}
		if name := dependencyName(line); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func mergeProject(base, extra pythonProject) pythonProject {
	if extra.RequiresPython != "" && base.RequiresPython == "" {
		base.RequiresPython = extra.RequiresPython
		base.RequiresPythonSource = extra.RequiresPythonSource
		base.RequiresPythonPointer = extra.RequiresPythonPointer
	}
	if extra.HasProjectTable {
		base.HasProjectTable = true
	}
	if extra.HasPackageTable {
		base.HasPackageTable = true
	}
	for name, dep := range extra.Dependencies {
		if _, exists := base.Dependencies[name]; exists {
			continue
		}
		base.Dependencies[name] = dep
	}
	for name := range extra.ToolTables {
		base.ToolTables[name] = struct{}{}
	}
	for name := range extra.ManagerTables {
		base.ManagerTables[name] = struct{}{}
	}
	return base
}

func setRequiresPython(parsed *pythonProject, version, source, pointer string) {
	if version == "" || parsed.RequiresPython != "" {
		return
	}
	parsed.RequiresPython = version
	parsed.RequiresPythonSource = source
	parsed.RequiresPythonPointer = pointer
}

func recordToolTable(parsed *pythonProject, rest string) {
	root, _, _ := strings.Cut(rest, ".")
	if root != "" {
		parsed.ToolTables[root] = struct{}{}
	}
	if manager, ok := managerTables[root]; ok {
		parsed.ManagerTables[manager] = struct{}{}
	}
	if strings.HasPrefix(rest, "poetry") {
		parsed.ManagerTables["poetry"] = struct{}{}
	}
}

func addDependencies(parsed *pythonProject, source, pointer string, values []string) {
	for _, value := range values {
		addDependency(parsed, source, pointer, value)
	}
}

func addKnownDependency(parsed *pythonProject, source, pointer, spec string) {
	name := dependencyName(spec)
	if !isKnownFramework(name) && !isKnownTool(name) {
		return
	}
	addDependency(parsed, source, pointer, name)
}

func addDependency(parsed *pythonProject, source, pointer, spec string) {
	name := dependencyName(spec)
	if name == "" {
		return
	}
	if _, exists := parsed.Dependencies[name]; exists {
		return
	}
	parsed.Dependencies[name] = depDeclaration{Name: name, Source: source + pointer}
}

func dependencyName(spec string) string {
	spec = strings.TrimSpace(spec)
	spec = strings.Trim(spec, `"'`)
	if spec == "" || spec == "." || strings.HasPrefix(spec, ".") || strings.HasPrefix(spec, "/") {
		return ""
	}
	for i, r := range spec {
		if r == '[' || r == ';' || r == ' ' || r == '<' || r == '>' || r == '=' || r == '~' || r == '!' || r == '@' {
			return normalizeDependency(spec[:i])
		}
	}
	return normalizeDependency(spec)
}

func normalizeDependency(name string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(name), "_", "-"))
}

func isKnownFramework(name string) bool {
	for _, framework := range knownFrameworks {
		if name == framework {
			return true
		}
	}
	return false
}

func isKnownTool(name string) bool {
	for _, tool := range knownTools {
		if name == tool || strings.HasPrefix(name, tool+"-") {
			return true
		}
	}
	return false
}

func hasDependency(parsed pythonProject, names ...string) bool {
	for _, name := range names {
		if _, ok := parsed.Dependencies[normalizeDependency(name)]; ok {
			return true
		}
	}
	return false
}

func poetryScalar(section *tomlSection, key string) string {
	if section == nil {
		return ""
	}
	return section.scalars[key]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func parseTOML(contents string) map[string]*tomlSection {
	contents = stripTOMLComments(contents)
	doc := map[string]*tomlSection{}
	current := sectionOf(doc, "")
	i := 0
	for i < len(contents) {
		for i < len(contents) && (contents[i] == ' ' || contents[i] == '\t' || contents[i] == '\n' || contents[i] == '\r') {
			i++
		}
		if i >= len(contents) {
			break
		}
		if contents[i] == '[' {
			header, next, ok := readTableHeader(contents, i)
			if !ok {
				break
			}
			current = sectionOf(doc, header)
			i = next
			continue
		}
		key, next, ok := readTOMLKey(contents, i)
		if !ok {
			i = skipTOMLLine(contents, i)
			continue
		}
		i = skipTOMLSpace(contents, next)
		if i >= len(contents) || contents[i] != '=' {
			i = skipTOMLLine(contents, i)
			continue
		}
		i = skipTOMLSpace(contents, i+1)
		value, kind, next := readTOMLValue(contents, i)
		i = next
		current.keys = appendUnique(current.keys, key)
		switch kind {
		case "string":
			if _, exists := current.scalars[key]; !exists {
				current.scalars[key] = value
			}
		case "array":
			current.arrays[key] = splitTOMLArray(value)
		}
	}
	return doc
}

func sectionOf(doc map[string]*tomlSection, name string) *tomlSection {
	if sec, ok := doc[name]; ok {
		return sec
	}
	sec := &tomlSection{
		scalars: make(map[string]string),
		arrays:  make(map[string][]string),
	}
	doc[name] = sec
	return sec
}

func readTableHeader(contents string, i int) (string, int, bool) {
	if i+1 < len(contents) && contents[i+1] == '[' {
		end := strings.Index(contents[i:], "]]")
		if end < 0 {
			return "", len(contents), false
		}
		header := strings.TrimSpace(contents[i+2 : i+end])
		return header, i + end + 2, true
	}
	end := strings.IndexByte(contents[i:], ']')
	if end < 0 {
		return "", len(contents), false
	}
	header := strings.TrimSpace(contents[i+1 : i+end])
	return header, i + end + 1, true
}

func readTOMLKey(contents string, i int) (string, int, bool) {
	if contents[i] == '"' || contents[i] == '\'' {
		value, next, ok := readQuoted(contents, i)
		return value, next, ok
	}
	start := i
	for i < len(contents) {
		r := contents[i]
		if r == '=' || r == ' ' || r == '\t' || r == '\n' || r == '.' {
			break
		}
		if !isBareKey(r) {
			return "", start, false
		}
		i++
	}
	if i == start {
		return "", start, false
	}
	return contents[start:i], i, true
}

func isBareKey(r byte) bool {
	return unicode.IsLetter(rune(r)) || unicode.IsDigit(rune(r)) || r == '_' || r == '-'
}

func readTOMLValue(contents string, i int) (string, string, int) {
	if i >= len(contents) {
		return "", "", i
	}
	switch contents[i] {
	case '"', '\'':
		value, next, ok := readQuoted(contents, i)
		if !ok {
			return "", "", skipTOMLLine(contents, i)
		}
		return value, "string", next
	case '[':
		body, next := readBalanced(contents, i, '[', ']')
		return body, "array", next
	case '{':
		_, next := readBalanced(contents, i, '{', '}')
		return "", "table", next
	default:
		start := i
		for i < len(contents) && contents[i] != '\n' && contents[i] != ',' {
			i++
		}
		return strings.TrimSpace(contents[start:i]), "string", i
	}
}

func readQuoted(contents string, i int) (string, int, bool) {
	quote := contents[i]
	triple := i+2 < len(contents) && contents[i+1] == quote && contents[i+2] == quote
	i++
	if triple {
		i += 2
	}
	var out strings.Builder
	for i < len(contents) {
		if triple && i+2 < len(contents) && contents[i] == quote && contents[i+1] == quote && contents[i+2] == quote {
			return out.String(), i + 3, true
		}
		if !triple && contents[i] == quote {
			return out.String(), i + 1, true
		}
		if contents[i] == '\\' && i+1 < len(contents) {
			out.WriteByte(contents[i+1])
			i += 2
			continue
		}
		out.WriteByte(contents[i])
		i++
	}
	return out.String(), i, false
}

func readBalanced(contents string, i int, open, closer byte) (string, int) {
	depth := 0
	start := i
	inString := byte(0)
	escaped := false
	for i < len(contents) {
		ch := contents[i]
		if inString != 0 {
			switch {
			case escaped:
				escaped = false
			case ch == '\\':
				escaped = true
			case ch == inString:
				inString = 0
			}
			i++
			continue
		}
		if ch == '"' || ch == '\'' {
			inString = ch
			i++
			continue
		}
		if ch == open {
			depth++
		}
		if ch == closer {
			depth--
			if depth == 0 {
				return contents[start+1 : i], i + 1
			}
		}
		i++
	}
	return contents[start:], i
}

func splitTOMLArray(body string) []string {
	var values []string
	i := 0
	for i < len(body) {
		for i < len(body) && (body[i] == ' ' || body[i] == '\t' || body[i] == '\n' || body[i] == '\r' || body[i] == ',') {
			i++
		}
		if i >= len(body) {
			break
		}
		if body[i] == '"' || body[i] == '\'' {
			value, next, ok := readQuoted(body, i)
			if !ok {
				break
			}
			values = append(values, value)
			i = next
			continue
		}
		if body[i] == '{' {
			_, next := readBalanced(body, i, '{', '}')
			i = next
			continue
		}
		start := i
		for i < len(body) && body[i] != ',' && body[i] != '\n' {
			i++
		}
		if value := strings.TrimSpace(body[start:i]); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func stripTOMLComments(contents string) string {
	var out strings.Builder
	inString := byte(0)
	escaped := false
	triple := false
	for i := 0; i < len(contents); i++ {
		ch := contents[i]
		if inString != 0 {
			out.WriteByte(ch)
			switch {
			case escaped:
				escaped = false
			case ch == '\\':
				escaped = true
			case triple && i+2 < len(contents) && ch == inString && contents[i+1] == inString && contents[i+2] == inString:
				out.WriteByte(contents[i+1])
				out.WriteByte(contents[i+2])
				i += 2
				inString = 0
				triple = false
			case !triple && ch == inString:
				inString = 0
			}
			continue
		}
		if ch == '"' || ch == '\'' {
			inString = ch
			triple = i+2 < len(contents) && contents[i+1] == ch && contents[i+2] == ch
			out.WriteByte(ch)
			continue
		}
		if ch == '#' {
			for i < len(contents) && contents[i] != '\n' {
				i++
			}
			if i < len(contents) {
				out.WriteByte('\n')
			}
			continue
		}
		out.WriteByte(ch)
	}
	return out.String()
}

var setupDependencyKeys = []string{
	"install_requires",
	"tests_require",
	"setup_requires",
	"extras_require",
}

func setupDependencyStrings(contents string) []string {
	var values []string
	i := 0
	for i < len(contents) {
		if contents[i] == '#' {
			i = skipTOMLLine(contents, i)
			continue
		}
		if contents[i] == '"' || contents[i] == '\'' {
			_, next, ok := readQuoted(contents, i)
			if !ok {
				break
			}
			i = next
			continue
		}
		key, next, ok := readSetupKeyword(contents, i)
		if !ok {
			i++
			continue
		}
		i = skipTOMLSpace(contents, next)
		if i >= len(contents) || contents[i] != '=' {
			i = next
			continue
		}
		i = skipTOMLSpace(contents, i+1)
		switch {
		case i < len(contents) && (contents[i] == '[' || contents[i] == '{' || contents[i] == '('):
			open := contents[i]
			var closer byte
			switch open {
			case '[':
				closer = ']'
			case '{':
				closer = '}'
			default:
				closer = ')'
			}
			body, after := readBalanced(contents, i, open, closer)
			if key == "extras_require" {
				values = append(values, quotedStringsInBracketLists(body)...)
			} else {
				values = append(values, quotedStrings(body)...)
			}
			i = after
		case i < len(contents) && (contents[i] == '"' || contents[i] == '\''):
			value, after, ok := readQuoted(contents, i)
			if !ok {
				return values
			}
			values = append(values, value)
			i = after
		default:
			i = next
		}
	}
	return values
}

func readSetupKeyword(contents string, i int) (string, int, bool) {
	if i > 0 && isBareKey(contents[i-1]) {
		return "", i, false
	}
	for _, key := range setupDependencyKeys {
		if !strings.HasPrefix(contents[i:], key) {
			continue
		}
		end := i + len(key)
		if end < len(contents) && isBareKey(contents[end]) {
			continue
		}
		return key, end, true
	}
	return "", i, false
}

func quotedStrings(contents string) []string {
	var values []string
	for i := 0; i < len(contents); i++ {
		if contents[i] != '"' && contents[i] != '\'' {
			continue
		}
		value, next, ok := readQuoted(contents, i)
		if !ok {
			break
		}
		values = append(values, value)
		i = next - 1
	}
	return values
}

func quotedStringsInBracketLists(contents string) []string {
	var values []string
	for i := 0; i < len(contents); {
		if contents[i] != '[' {
			i++
			continue
		}
		end := strings.IndexByte(contents[i:], ']')
		if end < 0 {
			break
		}
		values = append(values, quotedStrings(contents[i:i+end+1])...)
		i += end + 1
	}
	return values
}

func skipTOMLSpace(contents string, i int) int {
	for i < len(contents) && (contents[i] == ' ' || contents[i] == '\t') {
		i++
	}
	return i
}

func skipTOMLLine(contents string, i int) int {
	for i < len(contents) && contents[i] != '\n' {
		i++
	}
	if i < len(contents) {
		return i + 1
	}
	return i
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func depPointer(name string) string {
	return "/dependencies/" + pointerToken(name)
}

func pointerToken(value string) string {
	value = strings.ReplaceAll(value, "~", "~0")
	return strings.ReplaceAll(value, "/", "~1")
}
