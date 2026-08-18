package knowledge

import "strings"

// IsComposeUp reports whether inv starts Compose services
// (`docker compose up` or `docker-compose up`).
func IsComposeUp(inv Invocation) bool {
	switch inv.Executable {
	case "docker-compose":
		return firstPositional(inv.Args) == "up"
	case "docker":
		rest := dropLeadingFlags(inv.Args)
		if len(rest) == 0 || rest[0] != "compose" {
			return false
		}
		return firstPositional(rest[1:]) == "up"
	default:
		return false
	}
}

func firstPositional(args []string) string {
	for _, arg := range dropLeadingFlags(args) {
		if arg == "--" {
			return ""
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return arg
	}
	return ""
}
