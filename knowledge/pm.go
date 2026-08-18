package knowledge

import "strings"

// ManagerInvocation is a package-manager command after directory flags have
// been stripped. Script is set when the invocation runs a package.json script.
type ManagerInvocation struct {
	Manager string
	Script  string
	Args    []string
	Install bool
}

// ClassifyManager reports whether inv is an npm, pnpm, yarn, or bun command.
func ClassifyManager(inv Invocation) (ManagerInvocation, bool) {
	switch inv.Executable {
	case "npm", "pnpm", "yarn", "bun":
		return classifyManager(inv.Executable, inv.Args), true
	default:
		return ManagerInvocation{}, false
	}
}

func classifyManager(manager string, args []string) ManagerInvocation {
	_, rest := takeLeadingFlags(args)
	if len(rest) == 0 {
		return ManagerInvocation{Manager: manager, Install: isBareInstall(manager) && !isGlobalInstall(args)}
	}

	switch rest[0] {
	case "install", "i", "ci":
		return ManagerInvocation{Manager: manager, Install: !isGlobalInstall(args)}
	case "run", "run-script":
		script, scriptArgs := takeScriptName(rest[1:])
		if script == "" {
			return ManagerInvocation{Manager: manager}
		}
		return ManagerInvocation{Manager: manager, Script: script, Args: scriptArgs}
	}

	if manager == "npm" {
		switch rest[0] {
		case "test", "start", "stop", "restart":
			return ManagerInvocation{Manager: manager, Script: rest[0], Args: rest[1:]}
		}
		return ManagerInvocation{Manager: manager}
	}

	if isManagerBuiltin(manager, rest[0]) {
		return ManagerInvocation{Manager: manager}
	}
	return ManagerInvocation{Manager: manager, Script: rest[0], Args: rest[1:]}
}

func isBareInstall(manager string) bool {
	switch manager {
	case "yarn", "pnpm", "bun":
		return true
	default:
		return false
	}
}

func takeLeadingFlags(args []string) (flags, rest []string) {
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") && args[i] != "--" {
		i = skipManagerFlag(args, i)
	}
	return args[:i], args[i:]
}

func takeScriptName(args []string) (script string, rest []string) {
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") && args[i] != "--" {
		i = skipManagerFlag(args, i)
	}
	if i >= len(args) {
		return "", nil
	}
	return args[i], args[i+1:]
}

func skipManagerFlag(args []string, i int) int {
	name, _, hasValue := strings.Cut(args[i], "=")
	if hasValue {
		return i + 1
	}
	if managerFlagTakesValue(name) && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
		return i + 2
	}
	return i + 1
}

func managerFlagTakesValue(name string) bool {
	switch name {
	case "--filter", "--dir", "-C", "--prefix", "--cwd", "--reporter", "--loglevel":
		return true
	default:
		return false
	}
}

// IsGlobalInstall reports whether inv is `npm install -g` / `--global`.
// Those install a CI tool, not the repository's dependencies.
func IsGlobalInstall(inv Invocation) bool {
	return isGlobalInstall(inv.Args)
}

// IsRemoteGoInstall reports whether inv installs a remote Go module
// (`go install github.com/foo/bar@latest`). Those install a CI helper, not
// a repository package. `go install ./cmd/foo` is kept.
func IsRemoteGoInstall(inv Invocation) bool {
	if inv.Executable != "go" || len(inv.Args) < 2 || inv.Args[0] != "install" {
		return false
	}
	for _, arg := range inv.Args[1:] {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		pkg, _, _ := strings.Cut(arg, "@")
		if strings.HasPrefix(pkg, ".") || strings.HasPrefix(pkg, "/") {
			return false
		}
		first, _, _ := strings.Cut(pkg, "/")
		return strings.Contains(first, ".")
	}
	return false
}

// IsGoPlumbing reports whether inv is a Go diagnostic (`go env`, `go version`,
// `go help`) rather than a repository build or test command.
func IsGoPlumbing(inv Invocation) bool {
	if inv.Executable != "go" || len(inv.Args) == 0 {
		return false
	}
	switch inv.Args[0] {
	case "env", "version", "help":
		return true
	default:
		return false
	}
}

// IsToolPlumbing reports whether inv is a version/info/help probe of a
// toolchain (the milestone 4 `go version` / `go env` precedent) rather than
// a repository command.
func IsToolPlumbing(inv Invocation) bool {
	if IsGoPlumbing(inv) {
		return true
	}
	switch inv.Executable {
	case "docker":
		return isDockerPlumbing(inv.Args)
	case "docker-compose":
		return isVersionInfoHelp(inv.Args)
	case "node", "npm", "pnpm", "yarn", "bun", "python", "python3", "ruby", "java":
		return isVersionInfoHelp(inv.Args)
	default:
		return false
	}
}

func isDockerPlumbing(args []string) bool {
	rest := dropLeadingFlags(args)
	if len(rest) == 0 {
		return false
	}
	if rest[0] == "compose" {
		return isVersionInfoHelp(rest[1:])
	}
	return isVersionInfoHelp(rest)
}

func isVersionInfoHelp(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if strings.HasPrefix(arg, "-") {
			name, _, _ := strings.Cut(arg, "=")
			if name == "--version" || name == "-v" || name == "--help" || name == "-h" {
				return true
			}
			continue
		}
		switch arg {
		case "version", "info", "help":
			return true
		default:
			return false
		}
	}
	return false
}

func isGlobalInstall(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		name, _, _ := strings.Cut(arg, "=")
		if name == "-g" || name == "--global" {
			return true
		}
	}
	return false
}

func isManagerBuiltin(manager, name string) bool {
	switch manager {
	case "yarn":
		_, ok := yarnBuiltins[name]
		return ok
	case "pnpm":
		_, ok := pnpmBuiltins[name]
		return ok
	case "bun":
		_, ok := bunBuiltins[name]
		return ok
	default:
		return false
	}
}

var yarnBuiltins = map[string]struct{}{
	"add": {}, "audit": {}, "autoclean": {}, "bin": {}, "cache": {}, "check": {},
	"config": {}, "create": {}, "constraints": {}, "dedupe": {}, "dlx": {},
	"exec": {}, "explain": {}, "generate-lock-entry": {}, "global": {}, "help": {},
	"import": {}, "info": {}, "init": {}, "install": {}, "link": {}, "list": {},
	"login": {}, "logout": {}, "node": {}, "npm": {}, "outdated": {}, "owner": {},
	"pack": {}, "plugin": {}, "policies": {}, "publish": {}, "remove": {}, "run": {},
	"search": {}, "set": {}, "stage": {}, "tag": {}, "team": {}, "unlink": {},
	"unplug": {}, "upgrade": {}, "upgrade-interactive": {}, "version": {},
	"versions": {}, "why": {}, "workspace": {}, "workspaces": {},
}

var pnpmBuiltins = map[string]struct{}{
	"add": {}, "audit": {}, "bin": {}, "config": {}, "create": {}, "dedupe": {},
	"dlx": {}, "env": {}, "exec": {}, "fetch": {}, "help": {}, "import": {},
	"init": {}, "install": {}, "link": {}, "list": {}, "ls": {}, "outdated": {},
	"pack": {}, "patch": {}, "patch-commit": {}, "prune": {}, "publish": {},
	"rebuild": {}, "recursive": {}, "remove": {}, "root": {}, "run": {},
	"server": {}, "setup": {}, "store": {}, "uninstall": {}, "unlink": {},
	"update": {}, "upgrade": {}, "why": {},
}

var bunBuiltins = map[string]struct{}{
	"add": {}, "build": {}, "create": {}, "init": {}, "install": {}, "link": {},
	"pm": {}, "remove": {}, "rm": {}, "run": {}, "test": {}, "unlink": {},
	"update": {}, "x": {},
}
