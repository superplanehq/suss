package gha

import (
	"regexp"
	"strings"
)

var heredocStart = regexp.MustCompile(`<<-?\s*['"]?([A-Za-z_][A-Za-z0-9_]*)['"]?`)

// normalizeRunScript joins line continuations and collapses heredoc bodies so
// later shell splitting does not treat JavaScript or YAML blobs as commands.
func normalizeRunScript(script string) string {
	return collapseHeredocs(joinContinuations(script))
}

func joinContinuations(script string) string {
	lines := strings.Split(script, "\n")
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
	return strings.Join(joined, "\n")
}

func collapseHeredocs(script string) string {
	lines := strings.Split(script, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		out = append(out, lines[i])
		word, ok := heredocDelimiter(lines[i])
		if !ok {
			continue
		}
		i++
		for i < len(lines) && strings.TrimSpace(lines[i]) != word {
			i++
		}
	}
	return strings.Join(out, "\n")
}

func heredocDelimiter(line string) (string, bool) {
	match := heredocStart.FindStringSubmatch(line)
	if len(match) != 2 {
		return "", false
	}
	return match[1], true
}
