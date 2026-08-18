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
    environment:
      EMPTY_VAR:
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
	if !hasEnv(result, "EMPTY_VAR", false) {
		t.Fatalf("missing EMPTY_VAR without default in %+v", result.Findings)
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

func TestDetectResolvesComposeAliasesAndMergeKeys(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"docker-compose.yml": `
x-db-credentials: &db-credentials
  POSTGRES_USER: &db-user listmonk
  POSTGRES_PASSWORD: &db-password listmonk
  POSTGRES_DB: &db-name listmonk
services:
  app:
    image: listmonk/listmonk:latest
    environment:
      LISTMONK_db__user: *db-user
      LISTMONK_db__password: *db-password
      LISTMONK_db__database: *db-name
      LISTMONK_db__host: db
  db:
    image: postgres:17-alpine
    environment:
      <<: *db-credentials
      POSTGRES_HOST: db
`,
	})

	for _, name := range []string{"POSTGRES_USER", "POSTGRES_PASSWORD", "POSTGRES_DB", "LISTMONK_db__user", "LISTMONK_db__password", "LISTMONK_db__database"} {
		if !hasEnv(result, name, true) {
			t.Fatalf("missing %s default in %+v", name, result.Findings)
		}
	}
	if !hasEnv(result, "POSTGRES_HOST", true) {
		t.Fatalf("missing explicit POSTGRES_HOST after merge in %+v", result.Findings)
	}
	if envValueExposed(result) {
		t.Fatalf("environment values were exposed in %+v", result.Findings)
	}
	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if !ok || item.Requirement.Kind != plan.RequirementEnvironment {
			continue
		}
		if item.Requirement.Version != "" {
			t.Fatalf("environment %s carried a version value %q", item.Requirement.Name, item.Requirement.Version)
		}
		for _, evidence := range item.Requirement.Evidence {
			if strings.Contains(evidence.Description, "listmonk") {
				t.Fatalf("environment %s evidence leaked an assignment value: %+v", item.Requirement.Name, evidence)
			}
		}
	}
}

func TestDetectExtractsComposeInterpolationNames(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"compose.yaml": `
services:
  db:
    image: postgres:${POSTGRES_VERSION:-16}
    ports:
      - "${HOST_PORT}:5432"
    volumes:
      - ${DATA_DIR}:/var/lib/postgresql/data
    build:
      context: .
      args:
        VERSION: ${APP_VERSION:-1}
    environment:
      APP_PASSWORD: ${DB_PASSWORD}
      DATABASE_URL: postgres://app@db/app
      LITERAL: $$HOSTNAME
      HOME_DIR: $${HOME}
`,
	})

	if !hasRequirement(result, plan.RequirementService, "db", "16") {
		t.Fatalf("missing db 16 from interpolation default in %+v", result.Findings)
	}
	if !hasEnv(result, "DB_PASSWORD", false) {
		t.Fatalf("missing interpolated DB_PASSWORD in %+v", result.Findings)
	}
	if !hasEnv(result, "POSTGRES_VERSION", true) {
		t.Fatalf("missing POSTGRES_VERSION with default in %+v", result.Findings)
	}
	if !hasEnv(result, "HOST_PORT", false) {
		t.Fatalf("missing interpolated HOST_PORT from ports in %+v", result.Findings)
	}
	if !hasEnv(result, "DATA_DIR", false) {
		t.Fatalf("missing interpolated DATA_DIR from volumes in %+v", result.Findings)
	}
	if !hasEnv(result, "APP_VERSION", true) {
		t.Fatalf("missing interpolated APP_VERSION from build args in %+v", result.Findings)
	}
	if hasEnvName(result, "HOSTNAME") {
		t.Fatalf("escaped $$HOSTNAME was treated as interpolation in %+v", result.Findings)
	}
	if hasEnvName(result, "HOME") {
		t.Fatalf("escaped $${HOME} was treated as interpolation in %+v", result.Findings)
	}
	if envValueExposed(result) {
		t.Fatalf("interpolation values were exposed in %+v", result.Findings)
	}
	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if !ok || item.Requirement.Kind != plan.RequirementEnvironment {
			continue
		}
		if item.Requirement.Version != "" {
			t.Fatalf("environment %s carried a value %q", item.Requirement.Name, item.Requirement.Version)
		}
		for _, evidence := range item.Requirement.Evidence {
			if strings.Contains(evidence.Description, "secret") || strings.Contains(evidence.Pointer, "app@") {
				t.Fatalf("interpolation evidence leaked a value: %+v", evidence)
			}
			if strings.Contains(evidence.Description, "/var/lib/postgresql") || strings.Contains(evidence.Pointer, "5432") {
				t.Fatalf("interpolation evidence retained a field value: %+v", evidence)
			}
		}
	}
}

func TestDetectMergesComposeOverride(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"compose.yaml": `
services:
  db:
    image: postgres:${BASE_VERSION:-15}
    environment:
      POSTGRES_USER: app
      POSTGRES_DB: app
    ports:
      - "${HOST_PORT}:5432"
`,
		"compose.override.yaml": `
services:
  db:
    image: postgres:16
    environment:
      POSTGRES_USER: override
      POSTGRES_PASSWORD: secret
  cache:
    image: redis:7
`,
	})

	if hasRequirement(result, plan.RequirementService, "db", "15") {
		t.Fatalf("base image survived the override: %+v", result.Findings)
	}
	if !hasRequirement(result, plan.RequirementService, "db", "16") {
		t.Fatalf("missing overridden db 16 in %+v", result.Findings)
	}
	if versions := requirementVersions(result, plan.RequirementService, "db"); len(versions) != 1 {
		t.Fatalf("db versions = %v, want only the overridden image", versions)
	}
	if !hasRequirement(result, plan.RequirementService, "cache", "7") {
		t.Fatalf("missing override-only cache service in %+v", result.Findings)
	}
	if hasEnvName(result, "BASE_VERSION") {
		t.Fatalf("interpolation from the overridden image was kept: %+v", result.Findings)
	}
	if !hasEnv(result, "HOST_PORT", false) {
		t.Fatalf("missing HOST_PORT from the surviving base ports in %+v", result.Findings)
	}
	if !hasEnv(result, "POSTGRES_USER", true) {
		t.Fatalf("missing merged POSTGRES_USER in %+v", result.Findings)
	}
	if !hasEnv(result, "POSTGRES_DB", true) {
		t.Fatalf("missing base POSTGRES_DB after merge in %+v", result.Findings)
	}
	if !hasEnv(result, "POSTGRES_PASSWORD", true) {
		t.Fatalf("missing override POSTGRES_PASSWORD in %+v", result.Findings)
	}
	if envValueExposed(result) {
		t.Fatalf("merged environment values were exposed in %+v", result.Findings)
	}
	if got := requirementSource(result, plan.RequirementService, "cache", "7"); got != "compose.override.yaml" {
		t.Fatalf("cache evidence source = %q, want compose.override.yaml", got)
	}
	if got := requirementSource(result, plan.RequirementService, "db", "16"); got != "compose.override.yaml" {
		t.Fatalf("overridden db image source = %q, want compose.override.yaml", got)
	}
	if got := requirementSource(result, plan.RequirementEnvironment, "HOST_PORT", ""); got != "compose.yaml" {
		t.Fatalf("HOST_PORT source = %q, want compose.yaml", got)
	}
	if got := requirementSource(result, plan.RequirementEnvironment, "POSTGRES_DB", ""); got != "compose.yaml" {
		t.Fatalf("POSTGRES_DB source = %q, want compose.yaml", got)
	}
	if got := requirementSource(result, plan.RequirementEnvironment, "POSTGRES_PASSWORD", ""); got != "compose.override.yaml" {
		t.Fatalf("POSTGRES_PASSWORD source = %q, want compose.override.yaml", got)
	}
}

func TestDetectMergesComposeSequencesByUniqueness(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"compose.yaml": `
services:
  db:
    image: postgres:16
    ports:
      - "${HOST_PORT}:5432"
    volumes:
      - ${DATA_DIR}:/var/lib/postgresql/data
      - ${BACKUP_DIR}:/backup
    command: ["echo", "${OLD_CMD}"]
    dns:
      - ${DNS_A}
`,
		"compose.override.yaml": `
services:
  db:
    ports:
      - "5433:5433"
    volumes:
      - /var/data:/var/lib/postgresql/data
      - ${LOG_DIR}:/logs
    command: ["echo", "ok"]
    dns:
      - ${DNS_B}
`,
	})

	if !hasEnv(result, "HOST_PORT", false) {
		t.Fatalf("missing HOST_PORT after override appended a different port: %+v", result.Findings)
	}
	if hasEnvName(result, "DATA_DIR") {
		t.Fatalf("DATA_DIR survived replacement of the same volume target: %+v", result.Findings)
	}
	if !hasEnv(result, "BACKUP_DIR", false) {
		t.Fatalf("missing BACKUP_DIR from the unmerged volume: %+v", result.Findings)
	}
	if !hasEnv(result, "LOG_DIR", false) {
		t.Fatalf("missing LOG_DIR from the override volume: %+v", result.Findings)
	}
	if hasEnvName(result, "OLD_CMD") {
		t.Fatalf("command interpolation from the replaced sequence was kept: %+v", result.Findings)
	}
	if !hasEnv(result, "DNS_A", false) || !hasEnv(result, "DNS_B", false) {
		t.Fatalf("dns sequences were not appended: %+v", result.Findings)
	}
	if src, ptr := envEvidence(result, "DNS_A"); src != "compose.yaml" || ptr != "/services/db/dns/0" {
		t.Fatalf("DNS_A evidence = %s#%s, want compose.yaml#/services/db/dns/0", src, ptr)
	}
	if src, ptr := envEvidence(result, "DNS_B"); src != "compose.override.yaml" || ptr != "/services/db/dns/0" {
		t.Fatalf("DNS_B evidence = %s#%s, want compose.override.yaml#/services/db/dns/0", src, ptr)
	}
}

func TestDetectMergesServiceNamedLikeAnAttribute(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"compose.yaml": `
services:
  test:
    image: postgres:16
  ports:
    image: redis:7
`,
		"compose.override.yaml": `
services:
  test:
    environment:
      FOO: bar
  ports:
    environment:
      BAZ: qux
`,
	})

	if !hasRequirement(result, plan.RequirementService, "test", "16") {
		t.Fatalf("service test lost its base image: %+v", result.Findings)
	}
	if !hasRequirement(result, plan.RequirementService, "ports", "7") {
		t.Fatalf("service ports lost its base image: %+v", result.Findings)
	}
	if !hasEnv(result, "FOO", true) || !hasEnv(result, "BAZ", true) {
		t.Fatalf("override environment was not merged onto the named services: %+v", result.Findings)
	}
}

func TestDetectMergesUniqueVolumeEntries(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"compose.yaml": `
services:
  db:
    image: postgres:16
    volumes:
      - type: bind
        source: ${DATA_DIR}
        target: /var/lib/postgresql/data
`,
		"compose.override.yaml": `
services:
  db:
    volumes:
      - target: /var/lib/postgresql/data
        read_only: true
`,
	})

	if !hasEnv(result, "DATA_DIR", false) {
		t.Fatalf("DATA_DIR was dropped when the override merged the same volume target: %+v", result.Findings)
	}
}

func TestDetectIgnoresOverrideWithoutBase(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"compose.override.yaml": "services:\n  db:\n    image: postgres:16\n",
	})

	if hasRequirement(result, plan.RequirementService, "db", "16") {
		t.Fatalf("lone override was treated as an active stack: %+v", result.Findings)
	}
	if _, ok := commandByName(result)["start services"]; ok {
		t.Fatalf("lone override produced docker compose up: %+v", result.Findings)
	}
}

func TestDetectHonorsComposeResetAndOverrideTags(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"compose.yaml": `
services:
  db:
    image: postgres:16
    ports:
      - "${HOST_PORT}:5432"
    environment:
      FOO: bar
      KEEP: yes
`,
		"compose.override.yaml": `
services:
  db:
    ports: !reset []
    environment:
      FOO: !reset null
    command: !override
      - echo
      - ${NEW_CMD}
`,
	})

	if hasEnvName(result, "HOST_PORT") {
		t.Fatalf("!reset ports kept the base interpolation: %+v", result.Findings)
	}
	if hasEnvName(result, "FOO") {
		t.Fatalf("!reset environment key was retained: %+v", result.Findings)
	}
	if !hasEnv(result, "KEEP", true) {
		t.Fatalf("unrelated environment key was cleared by !reset: %+v", result.Findings)
	}
	if !hasEnv(result, "NEW_CMD", false) {
		t.Fatalf("missing interpolation from !override command: %+v", result.Findings)
	}
}

func TestDetectExpandsSequenceMergeKeys(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"compose.yaml": `
x-one: &one
  FOO: a
x-two: &two
  BAR: ${BAR_VAL}
services:
  web:
    image: nginx:latest
    environment:
      <<: [*one, *two]
      BAZ: c
`,
	})

	for _, name := range []string{"FOO", "BAR", "BAZ"} {
		if !hasEnv(result, name, true) {
			t.Fatalf("missing %s from sequence merge key in %+v", name, result.Findings)
		}
	}
	if !hasEnv(result, "BAR_VAL", false) {
		t.Fatalf("missing interpolated BAR_VAL from merged anchor in %+v", result.Findings)
	}
}

func TestDetectScansNestedAndQuotedInterpolations(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"compose.yaml": `
services:
  web:
    image: nginx:${PRIMARY:-${FALLBACK}}
    environment:
      SINGLE: '${LITERAL}'
      DOUBLE: "${DOUBLE_VAR}"
      PLAIN: ${PLAIN_VAR}
`,
	})

	if !hasEnv(result, "PRIMARY", true) {
		t.Fatalf("missing PRIMARY from nested interpolation in %+v", result.Findings)
	}
	if !hasEnv(result, "FALLBACK", false) {
		t.Fatalf("missing FALLBACK from nested interpolation in %+v", result.Findings)
	}
	if hasEnvName(result, "LITERAL") {
		t.Fatalf("single-quoted scalar was interpolated: %+v", result.Findings)
	}
	if !hasEnv(result, "DOUBLE_VAR", false) || !hasEnv(result, "PLAIN_VAR", false) {
		t.Fatalf("quoted or plain interpolations were missed: %+v", result.Findings)
	}
}

func TestDetectIgnoresInactiveComposeBases(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"compose.yaml":       "services:\n  web:\n    image: nginx:1.27\n",
		"compose.yml":        "services:\n  unused:\n    image: redis:7\n",
		"docker-compose.yml": "services:\n  leftover:\n    image: postgres:16\n",
	})

	if !hasRequirement(result, plan.RequirementService, "web", "1.27") {
		t.Fatalf("missing active compose.yaml service in %+v", result.Findings)
	}
	if hasRequirement(result, plan.RequirementService, "unused", "7") {
		t.Fatalf("inactive compose.yml was emitted as a separate stack: %+v", result.Findings)
	}
	if hasRequirement(result, plan.RequirementService, "leftover", "16") {
		t.Fatalf("inactive docker-compose.yml was emitted as a separate stack: %+v", result.Findings)
	}
}

func TestDetectDoesNotInterpolateComposeMappingKeys(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"compose.yaml": `
services:
  web:
    image: nginx:latest
    labels:
      "${NOT_INTERPOLATED}": value
    environment:
      - REAL_VAR=${REAL_VALUE}
`,
	})

	if hasEnvName(result, "NOT_INTERPOLATED") {
		t.Fatalf("mapping key was interpolated: %+v", result.Findings)
	}
	if !hasEnv(result, "REAL_VAR", true) {
		t.Fatalf("missing REAL_VAR from environment list in %+v", result.Findings)
	}
	if !hasEnv(result, "REAL_VALUE", false) {
		t.Fatalf("missing interpolated REAL_VALUE from environment list value in %+v", result.Findings)
	}
}

func TestDetectEmitsDockerRequirementPerComposeDirectory(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"frontend/compose.yaml": "services:\n  web:\n    image: node:22\n",
		"backend/compose.yaml":  "services:\n  api:\n    image: golang:1.26\n",
	})

	dirs := dockerRequirementDirs(result)
	if !slices.Equal(dirs, []string{"backend", "frontend"}) {
		t.Fatalf("docker requirement dirs = %v, want backend and frontend", dirs)
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

func dockerRequirementDirs(result provider.Result) []string {
	var dirs []string
	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if !ok || item.Requirement.Kind != plan.RequirementTool || item.Requirement.Name != "docker" {
			continue
		}
		dirs = append(dirs, item.ProjectPath)
	}
	slices.Sort(dirs)
	return dirs
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

func hasEnvName(result provider.Result, name string) bool {
	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if ok && item.Requirement.Kind == plan.RequirementEnvironment && item.Requirement.Name == name {
			return true
		}
	}
	return false
}

func envEvidence(result provider.Result, name string) (string, string) {
	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if !ok || item.Requirement.Kind != plan.RequirementEnvironment || item.Requirement.Name != name {
			continue
		}
		if len(item.Requirement.Evidence) == 0 {
			return "", ""
		}
		return item.Requirement.Evidence[0].Source, item.Requirement.Evidence[0].Pointer
	}
	return "", ""
}

func requirementSource(result provider.Result, kind plan.RequirementKind, name, version string) string {
	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if !ok || item.Requirement.Kind != kind || item.Requirement.Name != name {
			continue
		}
		if kind != plan.RequirementEnvironment && item.Requirement.Version != version {
			continue
		}
		if len(item.Requirement.Evidence) == 0 {
			return ""
		}
		return item.Requirement.Evidence[0].Source
	}
	return ""
}

func requirementVersions(result provider.Result, kind plan.RequirementKind, name string) []string {
	var versions []string
	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if !ok || item.Requirement.Kind != kind || item.Requirement.Name != name {
			continue
		}
		versions = append(versions, item.Requirement.Version)
	}
	return versions
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
