package envfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

func TestDetectReturnsNothingWithoutExampleFiles(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{".env": "SECRET=real\n"})
	if len(result.Findings) != 0 {
		t.Fatalf("Detect() = %+v, did not want to read .env", result)
	}
}

func TestDetectReadsNamesAndNeverExposesValues(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".env.example": `
# comment
DATABASE_URL=
export REDIS_URL=redis://localhost:6379
API_SECRET="changeme"
NOT_A_NAME
`,
	})

	if !hasEnv(result, "DATABASE_URL", false) {
		t.Fatalf("DATABASE_URL = missing or wrong flags in %+v", result.Findings)
	}
	if !hasEnv(result, "REDIS_URL", true) {
		t.Fatalf("REDIS_URL = missing or wrong flags in %+v", result.Findings)
	}
	if !hasEnv(result, "API_SECRET", true) {
		t.Fatalf("API_SECRET = missing or wrong flags in %+v", result.Findings)
	}
	if hasEnvName(result, "NOT_A_NAME") {
		t.Fatalf("NOT_A_NAME should be skipped, got %+v", result.Findings)
	}
	if valueExposed(result) {
		t.Fatalf("env values were exposed in %+v", result.Findings)
	}
}

func TestDetectStripsTrailingDotenvCommentsBeforeDefaults(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".env.example": "API_TOKEN= # supplied at runtime\nDATABASE_URL=postgres://localhost\n",
	})
	if !hasEnv(result, "API_TOKEN", false) {
		t.Fatalf("API_TOKEN = missing or treated as having a default: %+v", result.Findings)
	}
	if !hasEnv(result, "DATABASE_URL", true) {
		t.Fatalf("DATABASE_URL = missing or lost its default: %+v", result.Findings)
	}
}

func TestDetectMergesSampleAndExampleFiles(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		".env.example": "SHARED=\nONLY_EXAMPLE=1\n",
		".env.sample":  "SHARED=default\nONLY_SAMPLE=\n",
	})

	if !hasEnv(result, "SHARED", true) {
		t.Fatalf("SHARED should merge a default from .env.sample: %+v", result.Findings)
	}
	if !hasEnvName(result, "ONLY_EXAMPLE") || !hasEnvName(result, "ONLY_SAMPLE") {
		t.Fatalf("missing names in %+v", result.Findings)
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

func hasEnv(result provider.Result, name string, hasDefault bool) bool {
	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if !ok || item.Requirement.Kind != plan.RequirementEnvironment || item.Requirement.Name != name {
			continue
		}
		gotRequired := item.Requirement.IsRequired != nil && *item.Requirement.IsRequired
		gotDefault := item.Requirement.HasDefault != nil && *item.Requirement.HasDefault
		return gotRequired && gotDefault == hasDefault
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

func valueExposed(result provider.Result) bool {
	needles := []string{"redis://", "changeme"}
	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if !ok {
			continue
		}
		if item.Requirement.Version != "" {
			return true
		}
		for _, evidence := range item.Requirement.Evidence {
			for _, needle := range needles {
				if strings.Contains(evidence.Description, needle) || strings.Contains(evidence.Pointer, needle) {
					return true
				}
			}
		}
	}
	return false
}
