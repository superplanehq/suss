package compose

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/superplanehq/suss/knowledge"
	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

func TestDetectReturnsNothingWithoutComposeFiles(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{"README.md": "hello\n"})
	if len(result.Findings) != 0 {
		t.Fatalf("Detect() = %+v, want no findings", result)
	}
}

func TestDetectReadsServicesEnvironmentAndComposeUp(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"compose.yaml": `
services:
  postgres:
    image: postgres:16
    environment:
      POSTGRES_USER: app
      DATABASE_URL: postgres://app@postgres/app
  redis:
    image: redis:7
    environment:
      - REDIS_URL=redis://redis
  web:
    build: .
`,
	})

	if !hasRequirement(result, plan.RequirementTool, "docker", "") {
		t.Fatalf("missing docker tool in %+v", result.Findings)
	}
	if !hasRequirement(result, plan.RequirementService, "postgres", "16") {
		t.Fatalf("missing postgres 16 in %+v", result.Findings)
	}
	if !hasRequirement(result, plan.RequirementService, "redis", "7") {
		t.Fatalf("missing redis 7 in %+v", result.Findings)
	}
	if !hasRequirement(result, plan.RequirementService, "web", "") {
		t.Fatalf("missing build-only web service in %+v", result.Findings)
	}
	if !hasEnv(result, "DATABASE_URL", true) {
		t.Fatalf("missing DATABASE_URL default in %+v", result.Findings)
	}
	if !hasEnv(result, "REDIS_URL", true) {
		t.Fatalf("missing REDIS_URL default in %+v", result.Findings)
	}
	if envValueExposed(result) {
		t.Fatalf("environment values were exposed in %+v", result.Findings)
	}

	up := commandByName(result)["start services"]
	if deref(up.Run) != "docker compose up -d" {
		t.Fatalf("compose up = %+v, want docker compose up -d", up)
	}
	if up.Origin != plan.CommandInferred {
		t.Fatalf("compose up origin = %s, want inferred", up.Origin)
	}
	if !knowledge.IsComposeUp(knowledge.ParseScript(deref(up.Run))[0]) {
		t.Fatalf("compose up %q was not classified as compose up", deref(up.Run))
	}
}

func TestDetectRecordsIncludeLimitationAndSkipsDotDirectories(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"compose.yaml":         "include:\n  - extra.yaml\nservices:\n  db:\n    image: postgres:16\n",
		".hidden/compose.yaml": "services:\n  secret:\n    image: redis:7\n",
	})

	if hasRequirement(result, plan.RequirementService, "redis", "7") {
		t.Fatalf("hidden compose file was read: %+v", result.Findings)
	}
	if !slices.Contains(factValues(result, "provider.compose.limitation"), "include") {
		t.Fatalf("facts = %v, want include limitation", factValues(result, "provider.compose.limitation"))
	}
}

func TestDetectFindsNestedComposeFiles(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"deploy/docker-compose.yml": "services:\n  clickhouse:\n    image: clickhouse/clickhouse-server:24.3\n",
	})

	if !hasRequirement(result, plan.RequirementService, "clickhouse", "24.3") {
		t.Fatalf("missing nested clickhouse service in %+v", result.Findings)
	}
	up := commandByName(result)["start services"]
	if up.Directory != "deploy" {
		t.Fatalf("directory = %q, want deploy", up.Directory)
	}
}

func detectFiles(t *testing.T, files map[string]string) provider.Result {
	t.Helper()

	root := t.TempDir()
	for name, contents := range files {
		writeFile(t, filepath.Join(root, name), contents)
	}

	result, err := Provider{}.Detect(provider.Context{RepositoryRoot: root, ProjectPath: "."})
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	return result
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("os.MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
}

func commandByName(result provider.Result) map[string]plan.Command {
	out := make(map[string]plan.Command)
	for _, finding := range result.Findings {
		item, ok := finding.(plan.CommandFinding)
		if !ok {
			continue
		}
		out[item.Command.Name] = item.Command
	}
	return out
}

func hasRequirement(result provider.Result, kind plan.RequirementKind, name, version string) bool {
	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if !ok {
			continue
		}
		if item.Requirement.Kind == kind && item.Requirement.Name == name && item.Requirement.Version == version {
			return true
		}
	}
	return false
}

func hasEnv(result provider.Result, name string, hasDefault bool) bool {
	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if !ok || item.Requirement.Kind != plan.RequirementEnvironment || item.Requirement.Name != name {
			continue
		}
		if item.Requirement.HasDefault != nil && *item.Requirement.HasDefault == hasDefault {
			return true
		}
	}
	return false
}

func envValueExposed(result provider.Result) bool {
	needles := []string{"postgres://", "redis://", "app@"}
	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if !ok || item.Requirement.Kind != plan.RequirementEnvironment {
			continue
		}
		if item.Requirement.Version != "" {
			return true
		}
		for _, evidence := range item.Requirement.Evidence {
			for _, needle := range needles {
				if strings.Contains(evidence.Description, needle) || strings.Contains(evidence.Source, needle) {
					return true
				}
			}
		}
	}
	return false
}

func factValues(result provider.Result, name string) []string {
	var values []string
	for _, finding := range result.Findings {
		item, ok := finding.(plan.PropertyFinding)
		if !ok || item.Property.Name != name {
			continue
		}
		values = append(values, item.Property.Value)
	}
	return values
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
