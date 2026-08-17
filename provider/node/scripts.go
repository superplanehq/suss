package node

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/superplanehq/suss/knowledge"
	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

type scriptResult struct {
	findings    []plan.Finding
	ambiguities []plan.Ambiguity
}

func scriptCommands(ctx provider.Context, manifest packageManifest, choice packageManagerChoice) (scriptResult, error) {
	scripts := stringFields(manifest.Scripts)
	var result scriptResult
	for name, body := range scripts {
		command, ambiguity, err := declaredScript(ctx, name, body, choice)
		if err != nil {
			return scriptResult{}, err
		}
		result.findings = append(result.findings, plan.CommandFinding{
			ProjectPath: ctx.ProjectPath,
			Detector:    providerName,
			Command:     command,
		})
		if ambiguity != nil {
			result.ambiguities = append(result.ambiguities, *ambiguity)
		}
	}
	return result, nil
}

func declaredScript(ctx provider.Context, name, body string, choice packageManagerChoice) (plan.Command, *plan.Ambiguity, error) {
	source := ctx.SourcePath("package.json")
	pointer := "/scripts/" + name
	id, err := plan.NewCommandID(plan.CommandIdentity{
		ProjectPath: ctx.ProjectPath,
		Provider:    providerName,
		Source:      source,
		Pointer:     pointer,
	})
	if err != nil {
		return plan.Command{}, nil, err
	}

	evidence := []plan.Evidence{{
		Kind:    plan.EvidenceDeclaration,
		Source:  source,
		Pointer: pointer,
	}}
	command := plan.Command{
		ID:              id,
		Name:            name,
		Directory:       ctx.ProjectPath,
		Scope:           plan.ScopeProject,
		Origin:          plan.CommandDeclared,
		Confidence:      plan.ConfidenceHigh,
		Evidence:        evidence,
		Interpretations: scriptInterpretations(source, pointer, body),
		Variants:        []plan.CommandVariant{},
	}

	if choice.selected != "" {
		command.Run = stringPtr(scriptRun(choice.selected, name))
		return command, nil, nil
	}

	command.Run = nil
	ambiguity := plan.Ambiguity{
		Subject:    commandSubject(name),
		CommandID:  &id,
		Message:    fmt.Sprintf("The %s script is declared, but its package-manager invocation cannot be selected.", name),
		Candidates: scriptCandidates(ctx, name, choice),
	}
	return command, &ambiguity, nil
}

func scriptRun(manager, name string) string {
	switch manager {
	case "npm":
		switch name {
		case "test", "start", "stop", "restart":
			return "npm " + name
		default:
			return "npm run " + name
		}
	case "pnpm":
		return "pnpm run " + name
	case "yarn":
		return "yarn run " + name
	case "bun":
		return "bun run " + name
	default:
		return manager + " run " + name
	}
}

func scriptCandidates(ctx provider.Context, name string, choice packageManagerChoice) []plan.Candidate {
	declaration := plan.Evidence{
		Kind:    plan.EvidenceDeclaration,
		Source:  ctx.SourcePath("package.json"),
		Pointer: "/scripts/" + name,
	}
	var candidates []plan.Candidate
	for _, finding := range choice.findings {
		property, ok := finding.(plan.PropertyFinding)
		if !ok || property.Property.Kind != plan.PropertyPackageManager {
			continue
		}
		candidates = append(candidates, plan.Candidate{
			Value:    scriptRun(property.Property.Name, name),
			Evidence: append([]plan.Evidence{declaration}, property.Property.Evidence...),
		})
	}
	return candidates
}

func scriptInterpretations(source, pointer, body string) []plan.Interpretation {
	matches := knowledge.InterpretScript(body)
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

func commandSubject(name string) string {
	return "command." + subjectToken(name) + ".run"
}

func subjectToken(name string) string {
	var b strings.Builder
	lastHyphen := true
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastHyphen = false
		case r == '.' || r == '-':
			if !lastHyphen && b.Len() > 0 {
				b.WriteRune(r)
				lastHyphen = true
			}
		default:
			if !lastHyphen && b.Len() > 0 {
				b.WriteRune('-')
				lastHyphen = true
			}
		}
	}
	token := strings.Trim(b.String(), ".-")
	if token == "" || !unicode.IsLetter(rune(token[0])) {
		token = "script-" + token
		token = strings.Trim(token, ".-")
	}
	if token == "script" {
		return "script-unnamed"
	}
	return token
}
