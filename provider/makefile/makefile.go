// Package makefile detects GNU Make targets and uses recipe text as evidence.
// Variable expansion is limited to simple $(VAR) / ${VAR} assignments;
// functions, includes, and automatic variables are recorded as limitations.
package makefile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/superplanehq/suss/knowledge"
	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

const providerName = "make"

var makefileNames = []string{"GNUmakefile", "Makefile", "makefile"}

// Provider detects Makefile targets in one project root.
type Provider struct{}

// Name returns the stable provider identifier.
func (Provider) Name() string {
	return providerName
}

// Detect inspects one project root. It returns an empty result when the
// directory has no Makefile.
func (Provider) Detect(ctx provider.Context) (provider.Result, error) {
	name, contents, ok, err := readMakefile(ctx)
	if err != nil {
		return provider.Result{}, err
	}
	if !ok {
		return provider.Result{}, nil
	}

	parsed := parseMakefile(contents)
	source := ctx.SourcePath(name)

	var result provider.Result
	result.Findings = append(result.Findings, toolFinding(ctx, source, "make"))
	if parsed.usesDocker {
		result.Findings = append(result.Findings, toolFinding(ctx, source, "docker"))
	}
	for _, limitation := range parsed.limitations {
		result.Findings = append(result.Findings, limitationFinding(ctx, source, limitation))
	}

	for _, target := range parsed.targets {
		command, err := declaredTarget(ctx, source, target)
		if err != nil {
			return provider.Result{}, err
		}
		result.Findings = append(result.Findings, plan.CommandFinding{
			ProjectPath: ctx.ProjectPath,
			Detector:    providerName,
			Command:     command,
		})
	}
	return result, nil
}

func readMakefile(ctx provider.Context) (string, string, bool, error) {
	for _, name := range makefileNames {
		path := filepath.Join(ctx.ProjectDir(), name)
		contents, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", "", false, fmt.Errorf("read %s: %w", name, err)
		}
		return name, string(contents), true, nil
	}
	return "", "", false, nil
}

func declaredTarget(ctx provider.Context, source string, target makeTarget) (plan.Command, error) {
	pointer := jsonPointer("targets", target.Name)
	id, err := plan.NewCommandID(plan.CommandIdentity{
		ProjectPath: ctx.ProjectPath,
		Provider:    providerName,
		Source:      source,
		Pointer:     pointer,
	})
	if err != nil {
		return plan.Command{}, err
	}

	run := "make " + target.Name
	return plan.Command{
		ID:              id,
		Name:            target.Name,
		Run:             stringPtr(run),
		Directory:       ctx.ProjectPath,
		Scope:           plan.ScopeProject,
		Origin:          plan.CommandDeclared,
		Confidence:      plan.ConfidenceHigh,
		Evidence:        targetEvidence(source, pointer, target.Recipe),
		Interpretations: recipeInterpretations(source, pointer, target.Recipe),
		Variants:        []plan.CommandVariant{},
	}, nil
}

func targetEvidence(source, pointer, recipe string) []plan.Evidence {
	evidence := []plan.Evidence{{
		Kind:    plan.EvidenceDeclaration,
		Source:  source,
		Pointer: pointer,
	}}
	if summary := recipeSummary(recipe); summary != "" {
		evidence[0].Description = summary
	}
	return evidence
}

func recipeSummary(recipe string) string {
	invocations := knowledge.ParseScript(recipe)
	if len(invocations) == 0 {
		return ""
	}
	names := make([]string, 0, len(invocations))
	seen := make(map[string]struct{}, len(invocations))
	for _, inv := range invocations {
		if inv.Executable == "" || strings.ContainsAny(inv.Executable, "$(){}") {
			continue
		}
		if _, ok := seen[inv.Executable]; ok {
			continue
		}
		seen[inv.Executable] = struct{}{}
		names = append(names, inv.Executable)
	}
	if len(names) == 0 {
		return ""
	}
	if len(names) == 1 {
		return "The target recipe invokes " + names[0] + "."
	}
	return "The target recipe invokes " + joinAnd(names) + "."
}

func joinAnd(names []string) string {
	if len(names) == 1 {
		return names[0]
	}
	if len(names) == 2 {
		return names[0] + " and " + names[1]
	}
	return strings.Join(names[:len(names)-1], ", ") + ", and " + names[len(names)-1]
}

func recipeInterpretations(source, pointer, recipe string) []plan.Interpretation {
	matches := knowledge.InterpretScript(recipe)
	if len(matches) == 0 {
		return []plan.Interpretation{}
	}
	interpretations := make([]plan.Interpretation, 0, len(matches))
	for _, match := range matches {
		interpretations = append(interpretations, plan.Interpretation{
			Capability: match.Capability,
			Confidence: match.Confidence,
			Evidence: []plan.Evidence{{
				Kind:        plan.EvidenceDeclaration,
				Source:      source,
				Pointer:     pointer,
				Description: match.Description,
			}},
		})
	}
	return interpretations
}

func toolFinding(ctx provider.Context, source, name string) plan.Finding {
	return plan.RequirementFinding{
		ProjectPath: ctx.ProjectPath,
		Detector:    providerName,
		Requirement: plan.Requirement{
			Kind:       plan.RequirementTool,
			Name:       name,
			Confidence: plan.ConfidenceHigh,
			Evidence: []plan.Evidence{{
				Kind:   plan.EvidenceFile,
				Source: source,
			}},
		},
	}
}

func limitationFinding(ctx provider.Context, source, value string) plan.Finding {
	return plan.PropertyFinding{
		ProjectPath: ctx.ProjectPath,
		Detector:    providerName,
		Property: plan.Property{
			Kind:       plan.PropertyFact,
			Name:       "provider.make.limitation",
			Value:      value,
			Confidence: plan.ConfidenceHigh,
			Evidence: []plan.Evidence{{
				Kind:        plan.EvidenceFile,
				Source:      source,
				Description: limitationDescription(value),
			}},
		},
	}
}

func limitationDescription(value string) string {
	switch value {
	case "variable-expansion":
		return "Make functions, automatic variables, and predefined variables are not expanded."
	case "include":
		return "Included makefiles are detected but not expanded."
	case "define":
		return "define/endef blocks are detected but not expanded."
	case "conditionals":
		return "Make conditionals are detected; targets inside them are still enumerated."
	default:
		return "The Makefile uses a feature that is not fully interpreted."
	}
}

func jsonPointer(parts ...string) string {
	var b strings.Builder
	for _, part := range parts {
		b.WriteByte('/')
		b.WriteString(escapePointer(part))
	}
	return b.String()
}

func escapePointer(value string) string {
	value = strings.ReplaceAll(value, "~", "~0")
	return strings.ReplaceAll(value, "/", "~1")
}

func stringPtr(value string) *string {
	return &value
}
