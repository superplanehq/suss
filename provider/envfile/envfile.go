// Package envfile detects environment-variable names from example env files.
// Values are never stored or emitted.
package envfile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

const providerName = "envfile"

var envFileNames = []string{
	".env.example",
	".env.sample",
	".env.template",
}

// Provider detects example environment files in one project root.
type Provider struct{}

// Name returns the stable provider identifier.
func (Provider) Name() string {
	return providerName
}

// Detect inspects one project root for example env files.
func (Provider) Detect(ctx provider.Context) (provider.Result, error) {
	vars := make(map[string]envRequirement)
	var order []string

	for _, name := range envFileNames {
		contents, ok, err := readFile(ctx.ProjectDir(), name)
		if err != nil {
			return provider.Result{}, err
		}
		if !ok {
			continue
		}
		source := ctx.SourcePath(name)
		for _, item := range parseEnvFile(contents) {
			existing, seen := vars[item.Name]
			if !seen {
				order = append(order, item.Name)
				existing = envRequirement{
					name:       item.Name,
					isRequired: true,
					hasDefault: item.HasDefault,
				}
			}
			existing.hasDefault = existing.hasDefault || item.HasDefault
			existing.evidence = append(existing.evidence, plan.Evidence{
				Kind:    plan.EvidenceDeclaration,
				Source:  source,
				Pointer: "/" + item.Name,
			})
			vars[item.Name] = existing
		}
	}

	var result provider.Result
	for _, name := range order {
		item := vars[name]
		result.Findings = append(result.Findings, plan.RequirementFinding{
			ProjectPath: ctx.ProjectPath,
			Detector:    providerName,
			Requirement: plan.Requirement{
				Kind:       plan.RequirementEnvironment,
				Name:       item.name,
				IsRequired: boolPtr(item.isRequired),
				HasDefault: boolPtr(item.hasDefault),
				Confidence: plan.ConfidenceHigh,
				Evidence:   item.evidence,
			},
		})
	}
	return result, nil
}

type envRequirement struct {
	name       string
	isRequired bool
	hasDefault bool
	evidence   []plan.Evidence
}

type envVar struct {
	Name       string
	HasDefault bool
}

func parseEnvFile(contents string) []envVar {
	var out []envVar
	seen := make(map[string]struct{})
	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		line = strings.TrimSpace(line)
		name, value, found := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		if !found || !validEnvName(name) {
			continue
		}
		value = stripDotenvComment(value)
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, envVar{
			Name:       name,
			HasDefault: unquote(strings.TrimSpace(value)) != "",
		})
	}
	return out
}

func stripDotenvComment(value string) string {
	inSingle, inDouble := false, false
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '#':
			if !inSingle && !inDouble {
				return strings.TrimRight(value[:i], " \t")
			}
		}
	}
	return value
}

func unquote(value string) string {
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

func validEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if r == '_' || unicode.IsLetter(r) {
			continue
		}
		if unicode.IsDigit(r) && i > 0 {
			continue
		}
		return false
	}
	return true
}

func readFile(dir, name string) (string, bool, error) {
	contents, err := os.ReadFile(filepath.Join(dir, name))
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read %s: %w", name, err)
	}
	return string(contents), true, nil
}

func boolPtr(value bool) *bool {
	return &value
}
