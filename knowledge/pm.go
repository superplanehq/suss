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

// ClassifyManager reports whether inv is an npm, pnpm, yarn, bun, or composer command.
func ClassifyManager(inv Invocation) (ManagerInvocation, bool) {
	switch inv.Executable {
	case "npm", "pnpm", "yarn", "bun":
		return classifyManager(inv.Executable, inv.Args), true
	case "composer":
		return classifyComposer(inv.Args), true
	default:
		return ManagerInvocation{}, false
	}
}

func classifyComposer(args []string) ManagerInvocation {
	rest := takeLeadingFlags(args)
	if len(rest) == 0 {
		return ManagerInvocation{Manager: "composer"}
	}

	switch rest[0] {
	case "install", "i":
		return ManagerInvocation{Manager: "composer", Install: true}
	case "run-script", "run":
		script, scriptArgs := takeScriptName(rest[1:])
		if script == "" {
			return ManagerInvocation{Manager: "composer"}
		}
		return ManagerInvocation{Manager: "composer", Script: script, Args: scriptArgs}
	case "test":
		return ManagerInvocation{Manager: "composer", Script: "test", Args: rest[1:]}
	}
	if isComposerBuiltin(rest[0]) {
		return ManagerInvocation{Manager: "composer"}
	}
	return ManagerInvocation{Manager: "composer", Script: rest[0], Args: rest[1:]}
}

func classifyManager(manager string, args []string) ManagerInvocation {
	rest := takeLeadingFlags(args)
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

func takeLeadingFlags(args []string) []string {
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") && args[i] != "--" {
		i = skipManagerFlag(args, i)
	}
	return args[i:]
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
	case "--filter", "--dir", "-C", "--prefix", "--cwd", "--reporter", "--loglevel", "--working-dir":
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

// IsRemoteCargoInstall reports whether inv installs a named crates.io or git
// crate (`cargo install cargo-nextest`). Those provision CI tools. A path
// install or a bare `cargo install` of the current package is kept.
func IsRemoteCargoInstall(inv Invocation) bool {
	if inv.Executable != "cargo" {
		return false
	}
	args := normalizeCargoArgs(inv.Args)
	if len(args) == 0 || args[0] != "install" {
		return false
	}
	hasPath := false
	hasRemoteSource := false
	positional := 0
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		arg := rest[i]
		if arg == "--" {
			positional += len(rest) - i - 1
			break
		}
		name, value, hasValue := strings.Cut(arg, "=")
		switch name {
		case "--path":
			hasPath = true
			if !hasValue && i+1 < len(rest) {
				i++
			}
			continue
		case "--git", "--index", "--registry":
			hasRemoteSource = true
			if !hasValue && value == "" && i+1 < len(rest) && !strings.HasPrefix(rest[i+1], "-") {
				i++
			}
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		positional++
	}
	if hasPath {
		return false
	}
	return hasRemoteSource || positional > 0
}

// IsRemotePipInstall reports whether inv installs named PyPI packages rather
// than a local project or a requirements file. Named pip installs in CI
// provision tools; they do not install the repository's dependency set.
func IsRemotePipInstall(inv Invocation) bool {
	executable := inv.Executable
	args := inv.Args
	if executable == "uv" && len(args) >= 1 && args[0] == "pip" {
		args = args[1:]
		executable = "pip"
	}
	if executable != "pip" && executable != "pip3" {
		return false
	}
	args = dropLeadingFlags(args)
	if len(args) == 0 || args[0] != "install" {
		return false
	}
	rest := args[1:]
	hasRequirement := false
	hasLocal := false
	hasNamed := false
	for i := 0; i < len(rest); i++ {
		arg := rest[i]
		name, _, _ := strings.Cut(arg, "=")
		if name == "-r" || name == "--requirement" || name == "-c" || name == "--constraint" {
			hasRequirement = true
			continue
		}
		if name == "-e" || name == "--editable" {
			hasLocal = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if isLocalPythonInstall(arg) {
			hasLocal = true
			continue
		}
		hasNamed = true
	}
	return hasNamed && !hasRequirement && !hasLocal
}

func isLocalPythonInstall(arg string) bool {
	if arg == "." || strings.HasPrefix(arg, "./") || strings.HasPrefix(arg, "../") || strings.HasPrefix(arg, "/") || strings.HasPrefix(arg, ".[") {
		return true
	}
	if strings.Contains(arg, "://") {
		return false
	}
	if strings.ContainsAny(arg, "/\\") {
		return true
	}
	return strings.HasSuffix(arg, ".whl") || strings.HasSuffix(arg, ".tar.gz") || strings.HasSuffix(arg, ".zip") || strings.HasPrefix(arg, "requirements")
}

// IsRemoteGemInstall reports whether inv installs named gems rather than a
// local gem archive. Named gem installs in CI provision tools; they do not
// install the repository's Bundler dependency set.
func IsRemoteGemInstall(inv Invocation) bool {
	if inv.Executable != "gem" || len(inv.Args) < 2 || inv.Args[0] != "install" {
		return false
	}
	for _, arg := range inv.Args[1:] {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return !strings.HasPrefix(arg, ".") && !strings.HasPrefix(arg, "/") && !strings.HasSuffix(arg, ".gem")
	}
	return false
}

// IsSystemPackagePlumbing reports whether inv provisions operating-system
// packages in CI. These commands prepare the runner rather than invoke a
// repository task or install its language dependency set.
func IsSystemPackagePlumbing(inv Invocation) bool {
	executable := inv.Executable
	args := inv.Args
	if executable == "sudo" {
		args = dropLeadingFlags(args)
		if len(args) == 0 {
			return false
		}
		executable = canonicalizeExecutable(args[0])
		args = args[1:]
	}
	if executable != "apt-get" && executable != "apt" {
		return false
	}
	args = dropLeadingFlags(args)
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "install", "update", "upgrade", "dist-upgrade":
		return true
	default:
		return false
	}
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
	if IsGoPlumbing(inv) || IsRustPlumbing(inv) {
		return true
	}
	switch inv.Executable {
	case "docker":
		return isDockerPlumbing(inv.Args)
	case "docker-compose":
		return isVersionInfoHelp(inv.Args)
	case "node", "python", "python3", "ruby", "java", "pip", "pip3", "poetry", "uv", "pipenv", "pdm":
		return isFlagVersionHelp(inv.Args)
	case "php":
		return isPHPPlumbing(inv.Args)
	case "composer":
		// Composer uses -v for verbosity and -V / --version for its version.
		return isComposerVersionHelp(inv.Args)
	case "npm", "pnpm", "yarn", "bun":
		// `npm version` / `yarn version` bump the package version; only
		// `--version` / `-v` are probes.
		return isFlagVersionHelp(inv.Args)
	default:
		return false
	}
}

// IsRustPlumbing reports whether inv is rustup/toolchain wiring or a
// cargo/rustc version probe rather than a repository command.
func IsRustPlumbing(inv Invocation) bool {
	switch inv.Executable {
	case "rustup-init":
		return true
	case "rustup":
		return !isRustupRunWrapper(inv.Args)
	case "cargo":
		return isCargoVersionProbe(normalizeCargoArgs(inv.Args))
	case "rustc":
		return isRustcVersionProbe(inv.Args)
	default:
		return false
	}
}

func isRustupRunWrapper(args []string) bool {
	rest := dropLeadingFlags(args)
	return len(rest) >= 3 && rest[0] == "run"
}

func isCargoVersionProbe(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if strings.HasPrefix(arg, "-") {
			name, _, _ := strings.Cut(arg, "=")
			switch name {
			case "--version", "-V", "--help", "-h":
				return true
			default:
				// cargo -v / --verbose is not a version probe.
				continue
			}
		}
		switch arg {
		case "version", "help":
			return true
		default:
			return false
		}
	}
	return false
}

func isRustcVersionProbe(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if !strings.HasPrefix(arg, "-") {
			return false
		}
		name, _, _ := strings.Cut(arg, "=")
		if name == "--version" || name == "--help" || name == "-h" {
			return true
		}
		if rustcShortFlagHasVersion(name) {
			return true
		}
	}
	return false
}

func rustcShortFlagHasVersion(name string) bool {
	if !strings.HasPrefix(name, "-") || strings.HasPrefix(name, "--") {
		return false
	}
	return strings.ContainsRune(name, 'V')
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
			if isVersionHelpFlag(arg) {
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

func isFlagVersionHelp(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if !strings.HasPrefix(arg, "-") {
			return false
		}
		if isVersionHelpFlag(arg) {
			return true
		}
	}
	return false
}

func isVersionHelpFlag(arg string) bool {
	name, _, _ := strings.Cut(arg, "=")
	return name == "--version" || name == "-v" || name == "--help" || name == "-h"
}

func isPHPPlumbing(args []string) bool {
	if isFlagVersionHelp(args) {
		return true
	}
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if !strings.HasPrefix(arg, "-") {
			return false
		}
		name, _, _ := strings.Cut(arg, "=")
		if isPHPDiagnosticFlag(name) {
			return true
		}
	}
	return false
}

func isPHPDiagnosticFlag(name string) bool {
	switch name {
	case "-i", "--info", "--ini", "-m", "--modules":
		return true
	default:
		return false
	}
}

func isComposerVersionHelp(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if !strings.HasPrefix(arg, "-") {
			return false
		}
		name, _, _ := strings.Cut(arg, "=")
		if name == "--version" || name == "-V" || name == "--help" || name == "-h" {
			return true
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

func isComposerBuiltin(name string) bool {
	_, ok := composerBuiltins[name]
	return ok
}

var composerBuiltins = map[string]struct{}{
	"about": {}, "archive": {}, "audit": {}, "browse": {}, "bump": {},
	"cc": {}, "check-platform-reqs": {}, "clear-cache": {}, "clearcache": {},
	"completion": {}, "config": {}, "create-project": {}, "depends": {}, "diagnose": {},
	"dump-autoload": {}, "dumpautoload": {}, "exec": {}, "fund": {},
	"global": {}, "help": {}, "home": {}, "i": {}, "info": {}, "init": {},
	"install": {}, "licenses": {}, "list": {}, "outdated": {}, "prohibits": {},
	"r": {}, "reinstall": {}, "remove": {}, "require": {}, "rm": {},
	"run": {}, "run-script": {}, "search": {}, "self-update": {}, "selfupdate": {},
	"show": {}, "status": {}, "suggests": {}, "u": {}, "uninstall": {},
	"update": {}, "upgrade": {}, "validate": {}, "why": {}, "why-not": {},
}

var bunBuiltins = map[string]struct{}{
	"add": {}, "build": {}, "create": {}, "init": {}, "install": {}, "link": {},
	"pm": {}, "remove": {}, "rm": {}, "run": {}, "test": {}, "unlink": {},
	"update": {}, "x": {},
}
