package semaphore

import (
	"os"
	"path"
	"path/filepath"
	"strings"
)

func resolveDirectory(repositoryRoot, base, relative string) string {
	base = normalizeDirectory(base)
	relative = strings.TrimSpace(relative)
	if relative == "" {
		return base
	}

	absolute := filepath.Join(repositoryRoot, filepath.FromSlash(base), filepath.FromSlash(relative))
	resolved, err := filepath.Rel(repositoryRoot, absolute)
	if err != nil || resolved == ".." || strings.HasPrefix(resolved, ".."+string(filepath.Separator)) {
		return base
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		return base
	}
	return normalizeDirectory(filepath.ToSlash(resolved))
}

func normalizeDirectory(value string) string {
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
	var pointer strings.Builder
	for _, part := range parts {
		pointer.WriteByte('/')
		part = strings.ReplaceAll(part, "~", "~0")
		pointer.WriteString(strings.ReplaceAll(part, "/", "~1"))
	}
	return pointer.String()
}
