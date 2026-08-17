package node

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

const providerName = "node"

// Provider detects Node.js projects from package.json and related files.
type Provider struct{}

// Name returns the stable provider identifier.
func (Provider) Name() string {
	return providerName
}

// Detect inspects one project root. It returns an empty result when the
// directory has no package.json.
func (Provider) Detect(ctx provider.Context) (provider.Result, error) {
	manifest, ok, err := readManifest(ctx)
	if err != nil {
		return provider.Result{}, err
	}
	if !ok {
		return provider.Result{}, nil
	}

	var result provider.Result
	result.Findings = append(result.Findings, languageFindings(ctx, manifest)...)

	choice, err := choosePackageManager(ctx, manifest)
	if err != nil {
		return provider.Result{}, err
	}
	result.Findings = append(result.Findings, choice.findings...)
	result.Ambiguities = append(result.Ambiguities, choice.ambiguities...)
	result.Conflicts = append(result.Conflicts, choice.conflicts...)

	install, err := installCommand(ctx, choice)
	if err != nil {
		return provider.Result{}, err
	}
	if install.command != nil {
		result.Findings = append(result.Findings, plan.CommandFinding{
			ProjectPath: ctx.ProjectPath,
			Detector:    providerName,
			Command:     *install.command,
		})
	}
	result.Ambiguities = append(result.Ambiguities, install.ambiguities...)

	scriptResult, err := scriptCommands(ctx, manifest, choice)
	if err != nil {
		return provider.Result{}, err
	}
	result.Findings = append(result.Findings, scriptResult.findings...)
	result.Ambiguities = append(result.Ambiguities, scriptResult.ambiguities...)

	runtime, err := runtimeRequirements(ctx, manifest)
	if err != nil {
		return provider.Result{}, err
	}
	result.Findings = append(result.Findings, runtime.findings...)
	result.Conflicts = append(result.Conflicts, runtime.conflicts...)

	result.Findings = append(result.Findings, toolFindings(ctx, manifest)...)
	return result, nil
}

type packageManifest struct {
	PackageManager       string                     `json:"packageManager"`
	Engines              map[string]json.RawMessage `json:"engines"`
	Scripts              map[string]json.RawMessage `json:"scripts"`
	Dependencies         map[string]json.RawMessage `json:"dependencies"`
	DevDependencies      map[string]json.RawMessage `json:"devDependencies"`
	OptionalDependencies map[string]json.RawMessage `json:"optionalDependencies"`
	PeerDependencies     map[string]json.RawMessage `json:"peerDependencies"`
	ESLintConfig         json.RawMessage            `json:"eslintConfig"`
	Jest                 json.RawMessage            `json:"jest"`
	Prettier             json.RawMessage            `json:"prettier"`
}

func readManifest(ctx provider.Context) (packageManifest, bool, error) {
	path := filepath.Join(ctx.ProjectDir(), "package.json")
	contents, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return packageManifest{}, false, nil
	}
	if err != nil {
		return packageManifest{}, false, fmt.Errorf("read package.json: %w", err)
	}

	var manifest packageManifest
	if err := json.Unmarshal(contents, &manifest); err != nil {
		return packageManifest{}, false, fmt.Errorf("parse %s: %w", ctx.SourcePath("package.json"), err)
	}
	return manifest, true, nil
}

func languageFindings(ctx provider.Context, manifest packageManifest) []plan.Finding {
	findings := []plan.Finding{
		plan.PropertyFinding{
			ProjectPath: ctx.ProjectPath,
			Detector:    providerName,
			Property: plan.Property{
				Kind:       plan.PropertyLanguage,
				Name:       "javascript",
				Confidence: plan.ConfidenceHigh,
				Evidence: []plan.Evidence{{
					Kind:   plan.EvidenceDeclaration,
					Source: ctx.SourcePath("package.json"),
				}},
			},
		},
	}

	typescriptEvidence := typescriptEvidence(ctx, manifest)
	if len(typescriptEvidence) == 0 {
		return findings
	}
	return append(findings, plan.PropertyFinding{
		ProjectPath: ctx.ProjectPath,
		Detector:    providerName,
		Property: plan.Property{
			Kind:       plan.PropertyLanguage,
			Name:       "typescript",
			Confidence: plan.ConfidenceHigh,
			Evidence:   typescriptEvidence,
		},
	})
}

func typescriptEvidence(ctx provider.Context, manifest packageManifest) []plan.Evidence {
	var evidence []plan.Evidence
	if fileExists(ctx.ProjectDir(), "tsconfig.json") {
		evidence = append(evidence, plan.Evidence{
			Kind:   plan.EvidenceConfiguration,
			Source: ctx.SourcePath("tsconfig.json"),
		})
	}
	if pointer, ok := dependencyPointer(manifest, "typescript"); ok {
		evidence = append(evidence, plan.Evidence{
			Kind:    plan.EvidenceDeclaration,
			Source:  ctx.SourcePath("package.json"),
			Pointer: pointer,
		})
	}
	return evidence
}

func dependencyPointer(manifest packageManifest, name string) (string, bool) {
	sections := []struct {
		field string
		deps  map[string]json.RawMessage
	}{
		{"/dependencies/", manifest.Dependencies},
		{"/devDependencies/", manifest.DevDependencies},
		{"/optionalDependencies/", manifest.OptionalDependencies},
		{"/peerDependencies/", manifest.PeerDependencies},
	}
	for _, section := range sections {
		if _, ok := stringFields(section.deps)[name]; ok {
			return section.field + name, true
		}
	}
	return "", false
}

func stringFields(raw map[string]json.RawMessage) map[string]string {
	out := make(map[string]string)
	for key, value := range raw {
		var text string
		if err := json.Unmarshal(value, &text); err != nil {
			continue
		}
		out[key] = text
	}
	return out
}

func hasJSONValue(raw json.RawMessage) bool {
	trimmed := string(raw)
	return len(raw) > 0 && trimmed != "null"
}

func fileExists(dir, name string) bool {
	info, err := os.Stat(filepath.Join(dir, name))
	return err == nil && !info.IsDir()
}

func readFile(dir, name string) (string, bool, error) {
	contents, err := os.ReadFile(filepath.Join(dir, name))
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return string(contents), true, nil
}

func stringPtr(value string) *string {
	return &value
}
