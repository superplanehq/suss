package python

import (
	"bufio"
	"slices"
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
	HasUVDefaultGroups    bool
	UVDefaultGroups       []string
	OptionalPoetryGroups  map[string]struct{}
}

const (
	depKindMain  = "main"
	depKindExtra = "extra"
	depKindGroup = "group"
)

type depOrigin struct {
	Kind    string
	Group   string
	Manager string
}

type depDeclaration struct {
	Name    string
	Source  string
	Origins []depOrigin
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
		Manifest:             "pyproject.toml",
		Dependencies:         make(map[string]depDeclaration),
		ToolTables:           make(map[string]struct{}),
		ManagerTables:        make(map[string]struct{}),
		OptionalPoetryGroups: make(map[string]struct{}),
	}

	if project, ok := doc["project"]; ok {
		parsed.HasProjectTable = true
		setRequiresPython(&parsed, project.scalars["requires-python"], "pyproject.toml", "/requires-python")
		addDependencies(&parsed, "pyproject.toml", "/project/dependencies", project.arrays["dependencies"], depOrigin{Kind: depKindMain})
	}
	for _, name := range sortedTOMLNames(doc) {
		section := doc[name]
		switch {
		case name == "project.optional-dependencies":
			for _, extra := range section.keys {
				addDependencies(&parsed, "pyproject.toml", "/project/optional-dependencies", section.arrays[extra], depOrigin{Kind: depKindExtra, Group: extra})
			}
		case strings.HasPrefix(name, "project.optional-dependencies."):
			extra := strings.TrimPrefix(name, "project.optional-dependencies.")
			for _, values := range section.arrays {
				addDependencies(&parsed, "pyproject.toml", "/project/optional-dependencies", values, depOrigin{Kind: depKindExtra, Group: extra})
			}
		case name == "dependency-groups":
			for _, group := range section.keys {
				addDependencies(&parsed, "pyproject.toml", "/dependency-groups", section.arrays[group], depOrigin{Kind: depKindGroup, Group: group})
			}
		case strings.HasPrefix(name, "dependency-groups."):
			group := strings.TrimPrefix(name, "dependency-groups.")
			for _, values := range section.arrays {
				addDependencies(&parsed, "pyproject.toml", "/dependency-groups", values, depOrigin{Kind: depKindGroup, Group: group})
			}
		case name == "tool.pdm.dev-dependencies" || strings.HasPrefix(name, "tool.pdm.dev-dependencies."):
			group := "dev"
			if rest, ok := strings.CutPrefix(name, "tool.pdm.dev-dependencies."); ok && rest != "" {
				group = rest
			}
			for _, key := range section.keys {
				originGroup := group
				if name == "tool.pdm.dev-dependencies" && key != "" {
					originGroup = key
				}
				addDependencies(&parsed, "pyproject.toml", "/tool/pdm/dev-dependencies", section.arrays[key], depOrigin{Kind: depKindGroup, Group: originGroup})
			}
			recordToolTable(&parsed, strings.TrimPrefix(name, "tool."))
		case name == "tool.uv":
			if _, ok := section.arrays["default-groups"]; ok {
				parsed.HasUVDefaultGroups = true
				parsed.UVDefaultGroups = append([]string(nil), section.arrays["default-groups"]...)
			}
			addDependencies(&parsed, "pyproject.toml", "/tool/uv/dev-dependencies", section.arrays["dev-dependencies"], depOrigin{Kind: depKindGroup, Group: "dev"})
			recordToolTable(&parsed, "uv")
		case strings.HasPrefix(name, "tool."):
			recordToolTable(&parsed, strings.TrimPrefix(name, "tool."))
		}
	}

	if poetry, ok := doc["tool.poetry.dependencies"]; ok {
		setRequiresPython(&parsed, poetryScalar(poetry, "python"), "pyproject.toml", "/tool/poetry/dependencies/python")
		for _, name := range poetry.keys {
			if name != "python" {
				addPoetryDependency(&parsed, doc, name)
			}
		}
	}
	for _, name := range sortedTOMLNames(doc) {
		rest, ok := strings.CutPrefix(name, "tool.poetry.dependencies.")
		if !ok || rest == "" || rest == "python" || strings.Contains(rest, ".") {
			continue
		}
		addPoetryDependency(&parsed, doc, rest)
	}
	applyPoetryExtras(&parsed, doc)
	if dev, ok := doc["tool.poetry.dev-dependencies"]; ok {
		for _, name := range dev.keys {
			if name != "python" {
				addDependency(&parsed, "pyproject.toml", "/tool/poetry/dev-dependencies", name, depOrigin{Kind: depKindGroup, Group: "dev"})
			}
		}
	}
	for _, name := range sortedTOMLNames(doc) {
		if !strings.HasPrefix(name, "tool.poetry.group.") {
			continue
		}
		rest := strings.TrimPrefix(name, "tool.poetry.group.")
		if !strings.Contains(rest, ".") {
			if isTOMLTrue(doc[name].scalars["optional"]) {
				parsed.OptionalPoetryGroups[rest] = struct{}{}
			}
			continue
		}
		if !strings.HasSuffix(name, ".dependencies") {
			continue
		}
		group := strings.TrimSuffix(rest, ".dependencies")
		for _, dep := range doc[name].keys {
			if dep != "python" {
				addDependency(&parsed, "pyproject.toml", "/tool/poetry/group", dep, depOrigin{Kind: depKindGroup, Group: group})
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
		origin := depOrigin{Kind: depKindMain}
		if table == "dev-packages" {
			origin = depOrigin{Kind: depKindGroup, Group: "dev"}
		}
		for _, name := range section.keys {
			addDependency(&parsed, "Pipfile", "/"+table, name, origin)
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
	for _, match := range setupDependencyEntries(contents) {
		origin := depOrigin{Kind: depKindMain}
		switch match.key {
		case "extras_require":
			origin = depOrigin{Kind: depKindExtra, Group: match.extra}
		case "tests_require":
			origin = depOrigin{Kind: depKindExtra}
		}
		addKnownDependency(&parsed, "setup.py", "/setup", match.value, origin)
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
		if existing, exists := base.Dependencies[name]; exists {
			existing.Origins = appendOrigins(existing.Origins, dep.Origins...)
			base.Dependencies[name] = existing
			continue
		}
		base.Dependencies[name] = dep
	}
	if extra.HasUVDefaultGroups && !base.HasUVDefaultGroups {
		base.HasUVDefaultGroups = true
		base.UVDefaultGroups = append([]string(nil), extra.UVDefaultGroups...)
	}
	if len(extra.OptionalPoetryGroups) > 0 {
		if base.OptionalPoetryGroups == nil {
			base.OptionalPoetryGroups = make(map[string]struct{})
		}
		for group := range extra.OptionalPoetryGroups {
			base.OptionalPoetryGroups[group] = struct{}{}
		}
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

func addDependencies(parsed *pythonProject, source, pointer string, values []string, origin depOrigin) {
	for _, value := range values {
		addDependency(parsed, source, pointer, value, origin)
	}
}

func addKnownDependency(parsed *pythonProject, source, pointer, spec string, origin depOrigin) {
	name := dependencyName(spec)
	if !isKnownFramework(name) && !isKnownTool(name) {
		return
	}
	addDependency(parsed, source, pointer, name, origin)
}

func addDependency(parsed *pythonProject, source, pointer, spec string, origin depOrigin) {
	name := dependencyName(spec)
	if name == "" {
		return
	}
	if origin.Kind == "" {
		origin.Kind = depKindMain
	}
	dep, exists := parsed.Dependencies[name]
	if !exists {
		dep = depDeclaration{Name: name, Source: source + pointer}
	}
	dep.Origins = appendOrigins(dep.Origins, origin)
	parsed.Dependencies[name] = dep
}

func appendOrigins(existing []depOrigin, additions ...depOrigin) []depOrigin {
	for _, addition := range additions {
		found := false
		for _, origin := range existing {
			if origin.Kind == addition.Kind && origin.Group == addition.Group && origin.Manager == addition.Manager {
				found = true
				break
			}
		}
		if !found {
			existing = append(existing, addition)
		}
	}
	return existing
}

func sortedTOMLNames(doc map[string]*tomlSection) []string {
	names := make([]string, 0, len(doc))
	for name := range doc {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
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

func hasInstallableDependency(project pythonProject, manager string, names ...string) bool {
	for _, name := range names {
		dep, ok := project.Dependencies[normalizeDependency(name)]
		if ok && depInstallable(project, manager, dep) {
			return true
		}
	}
	return false
}

func isTOMLTrue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1":
		return true
	default:
		return false
	}
}

func poetryScalar(section *tomlSection, key string) string {
	if section == nil {
		return ""
	}
	return section.scalars[key]
}

func addPoetryDependency(parsed *pythonProject, doc map[string]*tomlSection, name string) {
	origin := depOrigin{Kind: depKindMain}
	if nested, ok := doc["tool.poetry.dependencies."+name]; ok && isTOMLTrue(nested.scalars["optional"]) {
		origin = depOrigin{Kind: depKindExtra}
	}
	addDependency(parsed, "pyproject.toml", "/tool/poetry/dependencies", name, origin)
}

func applyPoetryExtras(parsed *pythonProject, doc map[string]*tomlSection) {
	if extras, ok := doc["tool.poetry.extras"]; ok {
		for _, extra := range extras.keys {
			addDependencies(parsed, "pyproject.toml", "/tool/poetry/extras", extras.arrays[extra], depOrigin{Kind: depKindExtra, Group: extra})
		}
	}
	for _, name := range sortedTOMLNames(doc) {
		extra, ok := strings.CutPrefix(name, "tool.poetry.extras.")
		if !ok || extra == "" {
			continue
		}
		for _, values := range doc[name].arrays {
			addDependencies(parsed, "pyproject.toml", "/tool/poetry/extras", values, depOrigin{Kind: depKindExtra, Group: extra})
		}
	}
}

func parseTOML(contents string) map[string]*tomlSection {
	contents = stripTOMLComments(contents)
	doc := map[string]*tomlSection{}
	section := ""
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
			section = header
			sectionOf(doc, header)
			i = next
			continue
		}
		key, next, quoted, ok := readTOMLKey(contents, i)
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
		assignTOML(doc, section, key, value, kind, quoted)
	}
	return doc
}

func assignTOML(doc map[string]*tomlSection, section, key, value, kind string, quoted bool) {
	if !quoted {
		if i := strings.LastIndexByte(key, '.'); i >= 0 {
			prefix := key[:i]
			leaf := key[i+1:]
			if prefix != "" && leaf != "" {
				if section != "" {
					section = section + "." + prefix
				} else {
					section = prefix
				}
				key = leaf
			}
		}
	}
	current := sectionOf(doc, section)
	current.keys = appendUnique(current.keys, key)
	switch kind {
	case "string":
		if _, exists := current.scalars[key]; !exists {
			current.scalars[key] = value
		}
	case "array":
		current.arrays[key] = splitTOMLArray(value)
	case "table":
		nested := key
		if section != "" {
			nested = section + "." + key
		}
		assignInlineTable(doc, nested, value)
	}
}

func assignInlineTable(doc map[string]*tomlSection, section, body string) {
	i := 0
	for i < len(body) {
		for i < len(body) && (body[i] == ' ' || body[i] == '\t' || body[i] == '\n' || body[i] == '\r' || body[i] == ',') {
			i++
		}
		if i >= len(body) {
			break
		}
		key, next, quoted, ok := readTOMLKey(body, i)
		if !ok {
			break
		}
		i = skipTOMLSpace(body, next)
		if i >= len(body) || body[i] != '=' {
			break
		}
		i = skipTOMLSpace(body, i+1)
		value, kind, next := readTOMLValue(body, i)
		i = next
		assignTOML(doc, section, key, value, kind, quoted)
	}
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

func readTOMLKey(contents string, i int) (key string, next int, quoted, ok bool) {
	if contents[i] == '"' || contents[i] == '\'' {
		value, next, ok := readQuoted(contents, i)
		return value, next, true, ok
	}
	start := i
	for i < len(contents) {
		r := contents[i]
		if r == '=' || r == ' ' || r == '\t' || r == '\n' {
			break
		}
		if r == '.' {
			if i+1 >= len(contents) || !isBareKey(contents[i+1]) {
				break
			}
			i++
			continue
		}
		if !isBareKey(r) {
			return "", start, false, false
		}
		i++
	}
	if i == start {
		return "", start, false, false
	}
	return contents[start:i], i, false, true
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
		body, next := readBalanced(contents, i, '{', '}')
		return body, "table", next
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

type setupDep struct {
	key, extra, value string
}

func setupDependencyEntries(contents string) []setupDep {
	var values []setupDep
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
				values = append(values, setupExtrasRequireEntries(body)...)
				i = after
				continue
			}
			for _, value := range quotedStrings(body) {
				values = append(values, setupDep{key: key, value: value})
			}
			i = after
		case i < len(contents) && (contents[i] == '"' || contents[i] == '\''):
			value, after, ok := readQuoted(contents, i)
			if !ok {
				return values
			}
			values = append(values, setupDep{key: key, value: value})
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

func setupExtrasRequireEntries(body string) []setupDep {
	var values []setupDep
	i := 0
	for i < len(body) {
		for i < len(body) && body[i] != '"' && body[i] != '\'' && !isBareKey(body[i]) {
			i++
		}
		if i >= len(body) {
			break
		}
		extra, next, ok := readSetupExtraKey(body, i)
		if !ok {
			i++
			continue
		}
		i = skipTOMLSpace(body, next)
		if i >= len(body) || (body[i] != ':' && body[i] != '=') {
			continue
		}
		i = skipTOMLSpace(body, i+1)
		if i >= len(body) || body[i] != '[' {
			continue
		}
		list, after := readBalanced(body, i, '[', ']')
		for _, value := range quotedStrings(list) {
			values = append(values, setupDep{key: "extras_require", extra: extra, value: value})
		}
		i = after
	}
	return values
}

func readSetupExtraKey(contents string, i int) (string, int, bool) {
	if contents[i] == '"' || contents[i] == '\'' {
		return readQuoted(contents, i)
	}
	start := i
	for i < len(contents) && isBareKey(contents[i]) {
		i++
	}
	if i == start {
		return "", start, false
	}
	return contents[start:i], i, true
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
