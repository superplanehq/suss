package elixir

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/superplanehq/suss/knowledge"
	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

type commandSpec struct {
	name        string
	run         string
	pointer     string
	origin      plan.CommandOrigin
	confidence  plan.Confidence
	evidence    []plan.Evidence
	invocations string
}

func commandFindings(ctx provider.Context, parsed mixProject) ([]plan.Finding, error) {
	declaredNames := make(map[string]struct{}, len(parsed.Aliases))
	var specs []commandSpec
	for _, alias := range parsed.Aliases {
		declaredNames[alias.Name] = struct{}{}
		specs = append(specs, aliasSpec(ctx, alias))
	}

	tasks, err := customTaskSpecs(ctx)
	if err != nil {
		return nil, err
	}
	for _, task := range tasks {
		declaredNames[task.name] = struct{}{}
		specs = append(specs, task)
	}

	inferred, err := inferredSpecs(ctx, parsed)
	if err != nil {
		return nil, err
	}
	for _, spec := range inferred {
		if _, declared := declaredNames[spec.name]; !declared {
			specs = append(specs, spec)
		}
	}

	findings := make([]plan.Finding, 0, len(specs))
	for _, spec := range specs {
		command, err := commandFromSpec(ctx, spec)
		if err != nil {
			return nil, err
		}
		findings = append(findings, plan.CommandFinding{ProjectPath: ctx.ProjectPath, Detector: providerName, Command: command})
	}
	return findings, nil
}

func aliasSpec(ctx provider.Context, alias mixAlias) commandSpec {
	pointer := "/aliases/" + pointerToken(alias.Name)
	return commandSpec{
		name:        alias.Name,
		run:         "mix " + alias.Name,
		pointer:     pointer,
		origin:      plan.CommandDeclared,
		confidence:  plan.ConfidenceHigh,
		evidence:    []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: ctx.SourcePath("mix.exs"), Pointer: pointer}},
		invocations: aliasInvocations(alias.Tasks),
	}
}

func aliasInvocations(tasks []string) string {
	commands := make([]string, 0, len(tasks))
	for _, task := range tasks {
		task = strings.TrimSpace(task)
		if task == "" {
			continue
		}
		if command, ok := strings.CutPrefix(task, "cmd "); ok {
			commands = append(commands, command)
			continue
		}
		commands = append(commands, "mix "+task)
	}
	return strings.Join(commands, " && ")
}

var customTaskModule = regexp.MustCompile(`\bdefmodule\s+Mix\.Tasks\.([A-Za-z0-9_.]+)\s+do\b`)
var (
	acronymBoundary = regexp.MustCompile(`([A-Z]+)([A-Z][a-z])`)
	wordBoundary    = regexp.MustCompile(`([a-z0-9])([A-Z])`)
)

func customTaskSpecs(ctx provider.Context) ([]commandSpec, error) {
	root := filepath.Join(ctx.ProjectDir(), "lib", "mix", "tasks")
	var specs []commandSpec
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return fs.SkipAll
		}
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(entry.Name(), ".ex") {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		match := customTaskModule.FindSubmatch(contents)
		if len(match) != 2 {
			return nil
		}
		name := mixTaskName(string(match[1]))
		relative, err := filepath.Rel(ctx.RepositoryRoot, path)
		if err != nil {
			return err
		}
		source := filepath.ToSlash(relative)
		specs = append(specs, commandSpec{
			name:       name,
			run:        "mix " + name,
			pointer:    "/module/" + pointerToken(string(match[1])),
			origin:     plan.CommandDeclared,
			confidence: plan.ConfidenceHigh,
			evidence:   []plan.Evidence{{Kind: plan.EvidenceDeclaration, Source: source, Pointer: "/module"}},
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("find custom Mix tasks: %w", err)
	}
	slices.SortFunc(specs, func(a, b commandSpec) int { return strings.Compare(a.name, b.name) })
	return specs, nil
}

func mixTaskName(moduleSuffix string) string {
	parts := strings.Split(moduleSuffix, ".")
	for index, part := range parts {
		part = acronymBoundary.ReplaceAllString(part, `${1}_${2}`)
		part = wordBoundary.ReplaceAllString(part, `${1}_${2}`)
		parts[index] = strings.ToLower(part)
	}
	return strings.Join(parts, ".")
}

func inferredSpecs(ctx provider.Context, parsed mixProject) ([]commandSpec, error) {
	source := ctx.SourcePath("mix.exs")
	convention := func(name, run, pointer, description string) commandSpec {
		return commandSpec{
			name:       name,
			run:        run,
			pointer:    pointer,
			origin:     plan.CommandInferred,
			confidence: plan.ConfidenceMedium,
			evidence: []plan.Evidence{
				{Kind: plan.EvidenceFile, Source: source},
				{Kind: plan.EvidenceConvention, Source: "elixir-ecosystem", Pointer: strings.TrimPrefix(pointer, "/#"), Description: description},
			},
			invocations: run,
		}
	}

	var specs []commandSpec
	if parsed.HasDependencies || fileExists(filepath.Join(ctx.ProjectDir(), "mix.lock")) {
		specs = append(specs, convention("deps.get", "mix deps.get", "/#dependencies", "Mix projects conventionally fetch dependencies with mix deps.get."))
	}
	specs = append(specs, convention("compile", "mix compile", "/#compile", "Mix projects conventionally compile with mix compile."))
	testSource, err := firstExUnitTest(ctx.ProjectDir())
	if err != nil {
		return nil, err
	}
	if testSource != "" {
		spec := convention("test", "mix test", "/#test", "Mix projects with ExUnit tests conventionally run them with mix test.")
		spec.confidence = plan.ConfidenceHigh
		spec.evidence = append(spec.evidence[:1], plan.Evidence{Kind: plan.EvidenceFile, Source: ctx.SourcePath(testSource)}, spec.evidence[1])
		specs = append(specs, spec)
	}
	if parsed.HasPhoenix {
		specs = append(specs, convention("phx.server", "mix phx.server", "/#server", "Phoenix projects conventionally start the development server with mix phx.server."))
	}
	return specs, nil
}

func firstExUnitTest(root string) (string, error) {
	testRoot := filepath.Join(root, "test")
	var first string
	err := filepath.WalkDir(testRoot, func(path string, entry fs.DirEntry, err error) error {
		if os.IsNotExist(err) && path == testRoot {
			return fs.SkipAll
		}
		if err != nil {
			return err
		}
		if first != "" {
			return fs.SkipAll
		}
		if entry.IsDir() {
			return nil
		}
		if strings.HasSuffix(entry.Name(), "_test.exs") {
			relative, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			first = filepath.ToSlash(relative)
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("find ExUnit tests: %w", err)
	}
	return first, nil
}

func commandFromSpec(ctx provider.Context, spec commandSpec) (plan.Command, error) {
	source := spec.evidence[0].Source
	id, err := plan.NewCommandID(plan.CommandIdentity{ProjectPath: ctx.ProjectPath, Provider: providerName, Source: source, Pointer: spec.pointer})
	if err != nil {
		return plan.Command{}, err
	}
	interpretations := interpretations(spec.evidence[0].Kind, source, spec.pointer, spec.invocations)
	return plan.Command{
		ID:              id,
		Name:            spec.name,
		Run:             stringPtr(spec.run),
		Directory:       ctx.ProjectPath,
		Scope:           plan.ScopeProject,
		Origin:          spec.origin,
		Confidence:      spec.confidence,
		Evidence:        spec.evidence,
		Interpretations: interpretations,
		Variants:        []plan.CommandVariant{},
	}, nil
}

func interpretations(kind plan.EvidenceKind, source, pointer, script string) []plan.Interpretation {
	matches := knowledge.InterpretScript(script)
	result := make([]plan.Interpretation, 0, len(matches))
	for _, match := range matches {
		result = append(result, plan.Interpretation{
			Capability: match.Capability,
			Confidence: match.Confidence,
			Evidence: []plan.Evidence{{
				Kind:        kind,
				Source:      source,
				Pointer:     pointer,
				Description: match.Description,
			}},
		})
	}
	return result
}
