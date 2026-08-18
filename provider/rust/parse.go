package rust

import (
	"strings"
)

type cargoManifest struct {
	Name                 string
	RustVersion          string
	RustVersionWorkspace bool
	HasPackage           bool
	HasWorkspace         bool
	WorkspaceRustVersion string
	Dependencies         []string
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
			if crate, ok := dependencyCrateFromTable(section); ok {
				addDependency(seenDeps, &parsed.Dependencies, crate)
			}
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

		if crate, ok := dependencyCrateFromKey(section, key); ok {
			addDependency(seenDeps, &parsed.Dependencies, crate)
		}
	}
	return parsed
}

// ParseToolchainFile reads a rust-toolchain or rust-toolchain.toml pin.
func ParseToolchainFile(contents string) string {
	return parseToolchainFile(contents).Channel
}

func parseToolchainFile(contents string) toolchainFile {
	trimmed := strings.TrimSpace(contents)
	if strings.HasPrefix(trimmed, "[") {
		return toolchainFile{Channel: tomlValueAt(contents, "toolchain.channel")}
	}
	return toolchainFile{Channel: firstVersionLine(contents)}
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
	key, value, ok = strings.Cut(line, "=")
	if !ok {
		return "", "", false
	}
	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" {
		return "", "", false
	}
	return unquoteTOMLKey(key), value, true
}

func unquoteTOMLKey(key string) string {
	if text, ok := tomlString(key); ok {
		return text
	}
	return key
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

func dependencyCrateFromTable(section string) (string, bool) {
	for _, prefix := range []string{"dependencies.", "workspace.dependencies."} {
		if name, ok := strings.CutPrefix(section, prefix); ok && name != "" && !strings.Contains(name, ".") {
			return name, true
		}
	}
	return "", false
}

func dependencyCrateFromKey(section, key string) (string, bool) {
	switch section {
	case "dependencies", "workspace.dependencies":
		if key != "" {
			return key, true
		}
	}
	return "", false
}

func addDependency(seen map[string]struct{}, deps *[]string, name string) {
	if name == "" {
		return
	}
	if _, ok := seen[name]; ok {
		return
	}
	seen[name] = struct{}{}
	*deps = append(*deps, name)
}

var rustFrameworks = map[string]string{
	"actix-web": "actix-web",
	"axum":      "axum",
	"rocket":    "rocket",
}

func frameworkNames(dependencies []string) []string {
	var names []string
	seen := make(map[string]struct{})
	for _, dep := range dependencies {
		framework, ok := rustFrameworks[dep]
		if !ok {
			continue
		}
		if _, dup := seen[framework]; dup {
			continue
		}
		seen[framework] = struct{}{}
		names = append(names, framework)
	}
	return names
}
