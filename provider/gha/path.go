package gha

import (
	"path"
	"path/filepath"
	"strings"
)

func resolveDirectory(repo, base, rel string) string {
	base = normalizeRel(base)
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return base
	}

	var abs string
	if filepath.IsAbs(rel) {
		abs = filepath.Clean(rel)
	} else {
		abs = filepath.Join(repo, filepath.FromSlash(base), filepath.FromSlash(rel))
	}

	relative, err := filepath.Rel(repo, abs)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return base
	}
	return normalizeRel(filepath.ToSlash(relative))
}

func normalizeRel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "." {
		return "."
	}
	value = path.Clean("/" + strings.TrimPrefix(value, "./"))
	value = strings.TrimPrefix(value, "/")
	if value == "" {
		return "."
	}
	return value
}

func jsonPointer(parts ...string) string {
	var b strings.Builder
	for _, part := range parts {
		b.WriteByte('/')
		b.WriteString(escapePointer(part))
	}
	return b.String()
}

func escapePointer(value string) string {
	value = strings.ReplaceAll(value, "~", "~0")
	return strings.ReplaceAll(value, "/", "~1")
}

func actionName(uses string) string {
	uses = strings.TrimSpace(uses)
	if uses == "" {
		return ""
	}
	if strings.HasPrefix(uses, "docker://") {
		return ""
	}
	if strings.HasPrefix(uses, "./") || strings.HasPrefix(uses, "/") {
		return uses
	}
	name, _, _ := strings.Cut(uses, "@")
	return name
}

func isLocalAction(uses string) bool {
	uses = strings.TrimSpace(uses)
	return strings.HasPrefix(uses, "./") || strings.HasPrefix(uses, "/")
}

func matrixAxis(expression string) (string, bool) {
	trimmed := strings.TrimSpace(expression)
	trimmed = strings.TrimPrefix(trimmed, "${{")
	trimmed = strings.TrimSuffix(trimmed, "}}")
	trimmed = strings.TrimSpace(trimmed)
	name, ok := strings.CutPrefix(trimmed, "matrix.")
	if !ok {
		return "", false
	}
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, " ${}") {
		return "", false
	}
	return name, true
}
