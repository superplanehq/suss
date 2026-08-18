package knowledge

import "strings"

// IsComposeUp reports whether inv starts Compose services
// (`docker compose up` or `docker-compose up`).
func IsComposeUp(inv Invocation) bool {
	switch inv.Executable {
	case "docker-compose":
		return composeSubcommand(inv.Args) == "up"
	case "docker":
		rest := dropLeadingFlags(inv.Args)
		if len(rest) == 0 || rest[0] != "compose" {
			return false
		}
		return composeSubcommand(rest[1:]) == "up"
	default:
		return false
	}
}

func composeSubcommand(args []string) string {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return ""
		}
		if name, ok := strings.CutPrefix(arg, "--"); ok {
			key, _, hasValue := strings.Cut(name, "=")
			if composeValueFlags["--"+key] && !hasValue {
				i++
			}
			continue
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			if composeValueFlags[arg] {
				i++
			}
			continue
		}
		return arg
	}
	return ""
}

var composeValueFlags = map[string]bool{
	"-f":                  true,
	"--file":              true,
	"-p":                  true,
	"--project-name":      true,
	"--profile":           true,
	"--project-directory": true,
	"--env-file":          true,
	"--ansi":              true,
	"--progress":          true,
	"--parallel":          true,
}
