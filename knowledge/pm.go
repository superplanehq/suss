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
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		i = skipManagerFlag(args, i)
	}
	return args[:i], args[i:]
}

func takeScriptName(args []string) (script string, rest []string) {
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") {
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

func isGlobalInstall(args []string) bool {
	for _, arg := range args {
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
