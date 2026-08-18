package ruby

import (
	"bufio"
	"regexp"
	"strings"
)

type gemfile struct {
	RubyVersion string
	Gems        map[string]gemDeclaration
}

type gemDeclaration struct {
	Name string
}

type gemfileLock struct {
	BundlerVersion string
}

var (
	gemDeclarationPattern = regexp.MustCompile(`(?m)^\s*gem\s*(?:\(\s*)?["']([^"']+)["']`)
	rubyVersionPattern    = regexp.MustCompile(`(?m)^\s*ruby\s*(?:\(\s*)?["']([^"']+)["']`)
)

func parseGemfile(contents string) gemfile {
	withoutComments := stripRubyComments(contents)
	parsed := gemfile{Gems: make(map[string]gemDeclaration)}
	if match := rubyVersionPattern.FindStringSubmatch(withoutComments); len(match) == 2 {
		parsed.RubyVersion = strings.TrimSpace(match[1])
	}
	for _, match := range gemDeclarationPattern.FindAllStringSubmatch(withoutComments, -1) {
		if len(match) != 2 {
			continue
		}
		name := strings.TrimSpace(match[1])
		if name != "" {
			parsed.Gems[name] = gemDeclaration{Name: name}
		}
	}
	return parsed
}

func stripRubyComments(contents string) string {
	var out strings.Builder
	inSingle := false
	inDouble := false
	escaped := false
	for index := 0; index < len(contents); index++ {
		char := contents[index]
		if inSingle || inDouble {
			out.WriteByte(char)
			switch {
			case escaped:
				escaped = false
			case char == '\\':
				escaped = true
			case inSingle && char == '\'':
				inSingle = false
			case inDouble && char == '"':
				inDouble = false
			}
			continue
		}
		switch char {
		case '\'':
			inSingle = true
			out.WriteByte(char)
		case '"':
			inDouble = true
			out.WriteByte(char)
		case '#':
			for index < len(contents) && contents[index] != '\n' {
				index++
			}
			if index < len(contents) {
				out.WriteByte('\n')
			}
		default:
			out.WriteByte(char)
		}
	}
	return out.String()
}

func parseGemfileLock(contents string) gemfileLock {
	scanner := bufio.NewScanner(strings.NewReader(contents))
	inBundledWith := false
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "BUNDLED WITH" {
			inBundledWith = true
			continue
		}
		if !inBundledWith || trimmed == "" {
			continue
		}
		if line == trimmed {
			inBundledWith = false
			continue
		}
		return gemfileLock{BundlerVersion: trimmed}
	}
	return gemfileLock{}
}

func gemPointer(name string) string {
	return "/gems/" + pointerToken(name)
}

func pointerToken(value string) string {
	value = strings.ReplaceAll(value, "~", "~0")
	return strings.ReplaceAll(value, "/", "~1")
}
