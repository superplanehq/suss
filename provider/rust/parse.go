package rust

import (
	"strings"
)

type cargoDependency struct {
	Name  string
	Key   string
	Table string
}

type cargoManifest struct {
	Name                 string
	RustVersion          string
	RustVersionWorkspace bool
	HasPackage           bool
	HasWorkspace         bool
	WorkspaceRustVersion string
	Dependencies         []cargoDependency
}

type toolchainFile struct {
	Channel string
}

func parseCargoTOML(contents string) cargoManifest {
	var parsed cargoManifest
	section := ""
	seenDeps := make(map[string]struct{})

	for _, raw := range strings.Split(contents, "\n") {
		line := stripTOMLComment(raw)
		if line == "" {
			continue
		}
		if table, ok := parseTOMLTable(line); ok {
			section = table
			if section == "package" || strings.HasPrefix(section, "package.") {
				parsed.HasPackage = true
			}
			if section == "workspace" || strings.HasPrefix(section, "workspace.") {
				parsed.HasWorkspace = true
			}
			if key, table, ok := dependencyCrateFromTable(section); ok {
				addDependency(seenDeps, &parsed.Dependencies, key, key, table)
			}
			continue
		}

		key, value, quoted, ok := parseTOMLField(line)
		if !ok {
			continue
		}
		fullKey := key
		if section != "" {
			fullKey = section + "." + key
		}
		if fullKey == "workspace" || strings.HasPrefix(fullKey, "workspace.") {
			parsed.HasWorkspace = true
		}

		switch fullKey {
		case "package.name":
			if name, ok := tomlString(value); ok && parsed.Name == "" {
				parsed.Name = name
			}
		case "package.rust-version":
			if version, ok := tomlString(value); ok && parsed.RustVersion == "" {
				parsed.RustVersion = version
			}
		case "package.rust-version.workspace":
			if flag, ok := tomlBool(value); ok {
				parsed.RustVersionWorkspace = flag
			}
		case "workspace.package.rust-version":
			if version, ok := tomlString(value); ok && parsed.WorkspaceRustVersion == "" {
				parsed.WorkspaceRustVersion = version
			}
		}

		if depKey, table, ok := dependencyCrateFromKey(section, key, quoted); ok {
			crate := depKey
			if pkg, found := tomlInlineString(value, "package"); found {
				crate = pkg
			}
			if !quoted {
				if _, field, cut := strings.Cut(key, "."); cut && field == "package" {
					if pkg, found := tomlString(value); found {
						crate = pkg
					}
				}
			}
			addDependency(seenDeps, &parsed.Dependencies, depKey, crate, table)
			if crate != depKey {
				setDependencyCrate(&parsed.Dependencies, table, depKey, crate)
			}
		}
		if depKey, table, ok := dependencyCrateFromTable(section); ok && key == "package" {
			if pkg, found := tomlString(value); found {
				setDependencyCrate(&parsed.Dependencies, table, depKey, pkg)
			}
		}
	}
	return parsed
}

// ParseToolchainFile reads a rust-toolchain or rust-toolchain.toml pin.
func ParseToolchainFile(contents string) string {
	return parseToolchainFile(contents).Channel
}

func parseToolchainFile(contents string) toolchainFile {
	if toolchainFileLooksLikeTOML(contents) {
		return toolchainFile{Channel: tomlValueAt(contents, "toolchain.channel")}
	}
	return toolchainFile{Channel: firstVersionLine(contents)}
}

func toolchainFileLooksLikeTOML(contents string) bool {
	for _, raw := range strings.Split(contents, "\n") {
		line := stripTOMLComment(raw)
		if line == "" {
			continue
		}
		return strings.HasPrefix(line, "[")
	}
	return false
}

func tomlValueAt(contents, path string) string {
	section := ""
	for _, raw := range strings.Split(contents, "\n") {
		line := stripTOMLComment(raw)
		if line == "" {
			continue
		}
		if table, ok := parseTOMLTable(line); ok {
			section = table
			continue
		}
		key, value, ok := parseTOMLAssignment(line)
		if !ok {
			continue
		}
		fullKey := key
		if section != "" {
			fullKey = section + "." + key
		}
		if fullKey == path {
			if text, ok := tomlString(value); ok {
				return text
			}
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstVersionLine(contents string) string {
	for _, line := range strings.Split(contents, "\n") {
		line = stripTOMLComment(line)
		if line == "" {
			continue
		}
		return line
	}
	return ""
}

func stripTOMLComment(line string) string {
	inSingle, inDouble := false, false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '#':
			if !inSingle && !inDouble {
				return strings.TrimSpace(line[:i])
			}
		}
	}
	return strings.TrimSpace(line)
}

func parseTOMLTable(line string) (string, bool) {
	if strings.HasPrefix(line, "[[") && strings.HasSuffix(line, "]]") {
		return strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "[["), "]]")), true
	}
	if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
		return strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")), true
	}
	return "", false
}

func parseTOMLAssignment(line string) (key, value string, ok bool) {
	key, value, _, ok = parseTOMLField(line)
	return
}

func parseTOMLField(line string) (key, value string, quoted, ok bool) {
	key, value, ok = strings.Cut(line, "=")
	if !ok {
		return "", "", false, false
	}
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" {
		return "", "", false, false
	}
	if text, qok := tomlString(key); qok {
		return text, value, true, true
	}
	return key, value, false, true
}

func tomlString(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if len(value) < 2 {
		return "", false
	}
	quote := value[0]
	if quote != '"' && quote != '\'' {
		return "", false
	}
	if value[len(value)-1] != quote {
		return "", false
	}
	return value[1 : len(value)-1], true
}

func tomlBool(value string) (bool, bool) {
	switch strings.TrimSpace(value) {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		return false, false
	}
}

func dependencyCrateFromTable(section string) (string, string, bool) {
	for _, table := range []string{"dependencies", "workspace.dependencies"} {
		prefix := table + "."
		if name, ok := strings.CutPrefix(section, prefix); ok && name != "" && !strings.Contains(name, ".") {
			return name, table, true
		}
	}
	return "", "", false
}

func dependencyCrateFromKey(section, key string, quoted bool) (string, string, bool) {
	switch section {
	case "dependencies", "workspace.dependencies":
		if key == "" {
			return "", "", false
		}
		if !quoted {
			if name, field, ok := strings.Cut(key, "."); ok && name != "" && field != "" {
				return name, section, true
			}
		}
		return key, section, true
	}
	return "", "", false
}

func tomlInlineString(value, field string) (string, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "{") {
		return "", false
	}
	inner := strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(value, "{")), "}")
	for _, part := range splitTopLevel(inner, ',') {
		key, fieldValue, ok := parseTOMLAssignment(strings.TrimSpace(part))
		if !ok || key != field {
			continue
		}
		return tomlString(fieldValue)
	}
	return "", false
}

func splitTopLevel(value string, sep byte) []string {
	var parts []string
	start := 0
	inSingle, inDouble := false, false
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		default:
			if value[i] == sep && !inSingle && !inDouble {
				parts = append(parts, value[start:i])
				start = i + 1
			}
		}
	}
	return append(parts, value[start:])
}

func addDependency(seen map[string]struct{}, deps *[]cargoDependency, key, crate, table string) {
	if key == "" {
		return
	}
	if crate == "" {
		crate = key
	}
	seenKey := table + "\x00" + key
	if _, ok := seen[seenKey]; ok {
		return
	}
	seen[seenKey] = struct{}{}
	*deps = append(*deps, cargoDependency{Name: crate, Key: key, Table: table})
}

func setDependencyCrate(deps *[]cargoDependency, table, key, crate string) {
	if crate == "" {
		return
	}
	for i := range *deps {
		if (*deps)[i].Table == table && (*deps)[i].Key == key {
			(*deps)[i].Name = crate
			return
		}
	}
}

var rustFrameworks = map[string]string{
	"actix-web": "actix-web",
	"axum":      "axum",
	"rocket":    "rocket",
}

func packageFrameworks(dependencies []cargoDependency) []cargoDependency {
	var found []cargoDependency
	seen := make(map[string]struct{})
	for _, dep := range dependencies {
		if dep.Table != "dependencies" {
			continue
		}
		framework, ok := rustFrameworks[dep.Name]
		if !ok {
			continue
		}
		if _, dup := seen[framework]; dup {
			continue
		}
		seen[framework] = struct{}{}
		found = append(found, cargoDependency{Name: framework, Key: dep.Key, Table: dep.Table})
	}
	return found
}
