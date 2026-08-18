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
		if cargoDirectoryFlagIsDynamic(args) {
			return "", inv
		}
		args, dir = stripFlag(args, "-C", dir)
		args, _ = stripFlag(args, "--manifest-path", "")
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
// is removed because it changes the working directory. --manifest-path is
// kept: Cargo still reads .cargo/config.toml from the original cwd.
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
	tokens := splitShellQuoted(redacted)
	var prefix []string
	i := 0
	for i < len(tokens) {
		name, _, ok := strings.Cut(unquoteShellToken(tokens[i]), "=")
		if !ok || name == "" || strings.ContainsAny(name, "/\\") {
			break
		}
		prefix = append(prefix, tokens[i])
		i++
	}
	rest := tokens[i:]
	cargoAt := -1
	for j, token := range rest {
		if canonicalizeExecutable(unquoteShellToken(token)) == "cargo" {
			cargoAt = j
			break
		}
	}
	if cargoAt < 0 {
		return redacted
	}
	head := rest[:cargoAt+1]
	args := rest[cargoAt+1:]
	args = stripFlagQuoted(args, "-C")
	out := append(append([]string{}, prefix...), head...)
	out = append(out, args...)
	return strings.Join(out, " ")
}

func stripFlagQuoted(args []string, name string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := unquoteShellToken(args[i])
		if arg == "--" {
			return append(out, args[i:]...)
		}
		if arg == name {
			if i+1 < len(args) {
				i++
			}
			continue
		}
		if strings.HasPrefix(arg, name+"=") {
			continue
		}
		out = append(out, args[i])
	}
	return out
}

func unquoteShellToken(token string) string {
	if len(token) < 2 {
		return token
	}
	quote := token[0]
	if (quote == '"' || quote == '\'') && token[len(token)-1] == quote {
		return token[1 : len(token)-1]
	}
	return token
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

func cargoDirectoryFlagIsDynamic(args []string) bool {
	_, dir := stripFlag(append([]string{}, args...), "-C", "")
	if isDynamicPath(dir) {
		return true
	}
	_, dir = stripFlag(append([]string{}, args...), "--manifest-path", "")
	return isDynamicPath(dir)
}

func isDynamicPath(path string) bool {
	path = strings.TrimSpace(path)
	return strings.Contains(path, "${{") || strings.Contains(path, "$")
}
