package elixir

import (
	"regexp"
	"strings"
)

type mixProject struct {
	ElixirVersion     string
	Aliases           []mixAlias
	HasDependencies   bool
	HasPhoenix        bool
	HasDialyzerConfig bool
}

type mixAlias struct {
	Name  string
	Tasks []string
}

var (
	elixirVersionPattern = regexp.MustCompile(`\belixir\s*:\s*"([^"]+)"`)
	dependencyPattern    = regexp.MustCompile(`\bdefp?\s+deps\b|\bdeps\s*:\s*deps\s*\(`)
	phoenixPattern       = regexp.MustCompile(`\{\s*:phoenix\s*,`)
	dialyzerPattern      = regexp.MustCompile(`\bdialyzer\s*:`)
	aliasesFunction      = regexp.MustCompile(`\bdefp?\s+aliases\b`)
	quotedStringPattern  = regexp.MustCompile(`"((?:\\.|[^"\\])*)"`)
)

func parseMixProject(contents string) mixProject {
	withoutComments := stripElixirComments(contents)
	parsed := mixProject{
		HasDependencies:   dependencyPattern.MatchString(withoutComments),
		HasPhoenix:        phoenixPattern.MatchString(withoutComments),
		HasDialyzerConfig: dialyzerPattern.MatchString(withoutComments),
	}
	if match := elixirVersionPattern.FindStringSubmatch(withoutComments); len(match) == 2 {
		parsed.ElixirVersion = match[1]
	}
	parsed.Aliases = parseAliases(withoutComments)
	return parsed
}

func stripElixirComments(contents string) string {
	var out strings.Builder
	inString := false
	escaped := false
	for i := 0; i < len(contents); i++ {
		char := contents[i]
		if inString {
			out.WriteByte(char)
			switch {
			case escaped:
				escaped = false
			case char == '\\':
				escaped = true
			case char == '"':
				inString = false
			}
			continue
		}
		if char == '"' {
			inString = true
			out.WriteByte(char)
			continue
		}
		if char == '#' {
			for i < len(contents) && contents[i] != '\n' {
				i++
			}
			if i < len(contents) {
				out.WriteByte('\n')
			}
			continue
		}
		out.WriteByte(char)
	}
	return out.String()
}

func parseAliases(contents string) []mixAlias {
	location := aliasesFunction.FindStringIndex(contents)
	if location == nil {
		return nil
	}
	body := contents[location[1]:]
	start := strings.IndexByte(body, '[')
	if start < 0 {
		return nil
	}
	list, ok := balancedSection(body[start:], '[', ']')
	if !ok {
		return nil
	}

	var aliases []mixAlias
	for _, entry := range splitTopLevel(list[1:len(list)-1], ',') {
		key, value, ok := cutTopLevel(entry, ':')
		if !ok {
			continue
		}
		name := parseAliasName(key)
		if name == "" {
			continue
		}
		aliases = append(aliases, mixAlias{Name: name, Tasks: quotedStrings(value)})
	}
	return aliases
}

func parseAliasName(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "\"") && strings.HasSuffix(raw, "\"") {
		return strings.Trim(raw, "\"")
	}
	raw = strings.TrimPrefix(raw, ":")
	for _, char := range raw {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || strings.ContainsRune("_.!?-", char) {
			continue
		}
		return ""
	}
	return raw
}

func quotedStrings(value string) []string {
	matches := quotedStringPattern.FindAllStringSubmatch(value, -1)
	stringsFound := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) == 2 {
			stringsFound = append(stringsFound, strings.ReplaceAll(match[1], `\"`, `"`))
		}
	}
	return stringsFound
}

func balancedSection(input string, open, closing byte) (string, bool) {
	depth := 0
	inString := false
	escaped := false
	for i := 0; i < len(input); i++ {
		char := input[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case char == '\\':
				escaped = true
			case char == '"':
				inString = false
			}
			continue
		}
		if char == '"' {
			inString = true
			continue
		}
		switch char {
		case open:
			depth++
		case closing:
			depth--
			if depth == 0 {
				return input[:i+1], true
			}
		}
	}
	return "", false
}

func splitTopLevel(input string, separator byte) []string {
	var parts []string
	start := 0
	depth := 0
	inString := false
	escaped := false
	for i := 0; i < len(input); i++ {
		char := input[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case char == '\\':
				escaped = true
			case char == '"':
				inString = false
			}
			continue
		}
		switch char {
		case '"':
			inString = true
		case '[', '{', '(':
			depth++
		case ']', '}', ')':
			depth--
		default:
			if char == separator && depth == 0 {
				parts = append(parts, strings.TrimSpace(input[start:i]))
				start = i + 1
			}
		}
	}
	if tail := strings.TrimSpace(input[start:]); tail != "" {
		parts = append(parts, tail)
	}
	return parts
}

func cutTopLevel(input string, separator byte) (string, string, bool) {
	parts := splitTopLevel(input, separator)
	if len(parts) < 2 {
		return "", "", false
	}
	return parts[0], strings.Join(parts[1:], string(separator)), true
}
