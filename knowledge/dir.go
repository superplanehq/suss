package knowledge

import "strings"

// StripDirectoryFlags removes package-manager working-directory flags from an
// invocation and returns the flag value when present. The original invocation
// is not modified. Unrecognized executables are returned unchanged.
func StripDirectoryFlags(inv Invocation) (dir string, canonical Invocation) {
	args := append([]string{}, inv.Args...)
	dir = ""
	switch inv.Executable {
	case "yarn":
		args, dir = stripFlag(args, "--cwd", dir)
	case "pnpm":
		args, dir = stripFlag(args, "--dir", dir)
		args, dir = stripFlag(args, "-C", dir)
	case "npm":
		args, dir = stripFlag(args, "--prefix", dir)
	case "composer":
		args, dir = stripFlag(args, "--working-dir", dir)
		args, dir = stripFlag(args, "-d", dir)
	case "cargo":
		args, dir = stripFlag(args, "-C", dir)
		args, dir = stripManifestPath(args, dir)
	}
	return dir, Invocation{Executable: inv.Executable, Args: args}
}

// CanonicalInvocation strips directory flags and Cargo global options so
// naming and interpretation see the subcommand first.
func CanonicalInvocation(inv Invocation) Invocation {
	_, canonical := StripDirectoryFlags(inv)
	if canonicalizeExecutable(canonical.Executable) == "cargo" {
		canonical.Args = normalizeCargoArgs(canonical.Args)
	}
	return canonical
}

// RewriteDirectoryFlags returns redacted command text that is executable
// from the directory implied by package-manager directory flags. Cargo -C
// and --manifest-path are removed so consumers do not resolve those paths
// twice.
func RewriteDirectoryFlags(raw string, inv Invocation) string {
	redacted := RedactAssignmentValues(raw)
	if canonicalizeExecutable(inv.Executable) != "cargo" {
		return redacted
	}
	dir, _ := StripDirectoryFlags(inv)
	if dir == "" {
		return redacted
	}
	return stripCargoDirectoryFlagsFromRun(redacted)
}

func stripCargoDirectoryFlagsFromRun(redacted string) string {
	tokens := splitShell(redacted)
	var prefix []string
	i := 0
	for i < len(tokens) {
		name, _, ok := strings.Cut(tokens[i], "=")
		if !ok || name == "" || strings.ContainsAny(name, "/\\") {
			break
		}
		prefix = append(prefix, tokens[i])
		i++
	}
	rest := tokens[i:]
	if len(rest) == 0 || canonicalizeExecutable(rest[0]) != "cargo" {
		return redacted
	}
	args := rest[1:]
	args, _ = stripFlag(args, "-C", "")
	args, _ = stripManifestPath(args, "")
	out := append(append([]string{}, prefix...), "cargo")
	out = append(out, args...)
	return strings.Join(out, " ")
}

func stripFlag(args []string, name, current string) ([]string, string) {
	out := make([]string, 0, len(args))
	dir := current
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			out = append(out, args[i:]...)
			return out, dir
		}
		if arg == name {
			if i+1 < len(args) {
				dir = args[i+1]
				i++
			}
			continue
		}
		prefix := name + "="
		if strings.HasPrefix(arg, prefix) {
			dir = strings.TrimPrefix(arg, prefix)
			continue
		}
		out = append(out, arg)
	}
	return out, dir
}

func stripManifestPath(args []string, current string) ([]string, string) {
	out, dir := stripFlag(args, "--manifest-path", current)
	if dir == current || dir == "" {
		return out, current
	}
	if parent, ok := cargoManifestDirectory(dir); ok {
		return out, parent
	}
	return out, dir
}

func cargoManifestDirectory(path string) (string, bool) {
	normalized := strings.ReplaceAll(strings.TrimSpace(path), "\\", "/")
	normalized = strings.TrimPrefix(normalized, "./")
	if !strings.HasSuffix(normalized, "Cargo.toml") {
		return "", false
	}
	if strings.EqualFold(normalized, "Cargo.toml") {
		return ".", true
	}
	parent := strings.TrimSuffix(normalized, "/Cargo.toml")
	if parent == "" || parent == "." {
		return ".", true
	}
	return parent, true
}
