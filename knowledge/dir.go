package knowledge

import (
	"path"
	"strings"
)

// StripDirectoryFlags removes working-directory flags from an invocation and
// returns the targeted directory when present. The original invocation is not
// modified. Unrecognized executables are returned unchanged.
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
	default:
		switch canonicalizeExecutable(inv.Executable) {
		case "mvn":
			args, dir = stripMavenDirectoryFlags(args)
		case "gradle":
			args, dir = stripGradleDirectoryFlags(args)
		}
	}
	return dir, Invocation{Executable: inv.Executable, Args: args}
}

func stripMavenDirectoryFlags(args []string) ([]string, string) {
	file := ""
	args, file = stripFlag(args, "--file", file)
	args, file = stripFlag(args, "-f", file)
	return args, mavenFileDirectory(file)
}

func mavenFileDirectory(file string) string {
	file = strings.TrimSpace(file)
	if file == "" {
		return ""
	}
	cleaned := path.Clean(file)
	switch strings.ToLower(path.Ext(cleaned)) {
	case ".xml", ".pom":
		return path.Dir(cleaned)
	default:
		return cleaned
	}
}

func stripGradleDirectoryFlags(args []string) ([]string, string) {
	dir := ""
	args, dir = stripFlag(args, "--project-dir", dir)
	args, dir = stripFlag(args, "-p", dir)
	file := ""
	args, file = stripFlag(args, "--build-file", file)
	args, file = stripFlag(args, "-b", file)
	if dir == "" {
		dir = fileDirectory(file)
	}
	return args, dir
}

func fileDirectory(file string) string {
	file = strings.TrimSpace(file)
	if file == "" {
		return ""
	}
	return path.Dir(path.Clean(file))
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
