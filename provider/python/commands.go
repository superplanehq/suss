package python

import (
	"fmt"
	"strings"

	"github.com/superplanehq/suss/knowledge"
	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

type commandSpec struct {
	name        string
	run         string
	pointer     string
	confidence  plan.Confidence
	evidence    []plan.Evidence
	invocations string
}

func commandFindings(ctx provider.Context, project pythonProject, choice managerChoice) ([]plan.Finding, error) {
	var specs []commandSpec
	if choice.selected != "" && choice.install != "" {
		specs = append(specs, installSpec(ctx, project, choice))
	}

	testSpec, ok, err := testCommandSpec(ctx, project)
	if err != nil {
		return nil, err
	}
	if ok {
		specs = append(specs, testSpec)
	}

	if spec, ok, err := serverCommandSpec(ctx, project); err != nil {
		return nil, err
	} else if ok {
		specs = append(specs, spec)
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

func installSpec(ctx provider.Context, project pythonProject, choice managerChoice) commandSpec {
	source := ctx.SourcePath(project.Manifest)
	description := fmt.Sprintf("%s-managed Python projects conventionally install dependencies with %s.", choice.selected, choice.install)
	spec := conventionSpec(source, "install dependencies", choice.install, "/#dependencies", plan.ConfidenceMedium, description)
	if lock := lockfileFor(choice, choice.selected); lock != "" {
		spec.evidence = addEvidenceAfterManifest(spec.evidence, plan.Evidence{
			Kind:   plan.EvidenceDeclaration,
			Source: ctx.SourcePath(lock),
		})
	} else if choice.selected == "pip" && fileExists(ctx.ProjectDir(), "requirements.txt") {
		spec.evidence = addEvidenceAfterManifest(spec.evidence, plan.Evidence{
			Kind:   plan.EvidenceDeclaration,
			Source: ctx.SourcePath("requirements.txt"),
		})
	}
	return spec
}

func testCommandSpec(ctx provider.Context, project pythonProject) (commandSpec, bool, error) {
	testFile, err := firstPythonTest(ctx.ProjectDir())
	if err != nil || testFile == "" {
		return commandSpec{}, false, err
	}
	source := ctx.SourcePath(project.Manifest)
	testEvidence := plan.Evidence{Kind: plan.EvidenceFile, Source: ctx.SourcePath(testFile)}

	if pytestEvidence := pytestEvidence(ctx, project); len(pytestEvidence) > 0 {
		spec := conventionSpec(source, "test", "pytest", "/#test", plan.ConfidenceHigh, "Python projects with pytest conventionally run tests with pytest.")
		spec.evidence = addEvidenceAfterManifest(spec.evidence, append([]plan.Evidence{testEvidence}, pytestEvidence...)...)
		return spec, true, nil
	}

	if managePy, err := firstManagePy(ctx.ProjectDir()); err != nil {
		return commandSpec{}, false, err
	} else if managePy != "" && hasDependency(project, "django") {
		run := "python " + managePy + " test"
		spec := conventionSpec(source, "test", run, "/#test", plan.ConfidenceHigh, "Django applications with test files conventionally run them with manage.py test.")
		spec.evidence = addEvidenceAfterManifest(spec.evidence, testEvidence, plan.Evidence{Kind: plan.EvidenceFile, Source: ctx.SourcePath(managePy)})
		return spec, true, nil
	}

	spec := conventionSpec(source, "test", "python -m unittest", "/#test", plan.ConfidenceMedium, "Python projects with test files and no pytest signal conventionally run unittest.")
	spec.evidence = addEvidenceAfterManifest(spec.evidence, testEvidence)
	return spec, true, nil
}

func serverCommandSpec(ctx provider.Context, project pythonProject) (commandSpec, bool, error) {
	source := ctx.SourcePath(project.Manifest)
	if managePy, err := firstManagePy(ctx.ProjectDir()); err != nil {
		return commandSpec{}, false, err
	} else if managePy != "" && hasDependency(project, "django") {
		run := "python " + managePy + " runserver"
		spec := conventionSpec(source, "server", run, "/#server", plan.ConfidenceMedium, "Django applications conventionally start the development server with manage.py runserver.")
		spec.evidence = addEvidenceAfterManifest(spec.evidence, plan.Evidence{Kind: plan.EvidenceFile, Source: ctx.SourcePath(managePy)})
		return spec, true, nil
	}

	if hasDependency(project, "flask") {
		if app := flaskApplicationFile(ctx); app != "" {
			spec := conventionSpec(source, "server", "flask run", "/#server", plan.ConfidenceMedium, "Flask applications conventionally start the development server with flask run.")
			spec.evidence = addEvidenceAfterManifest(spec.evidence, plan.Evidence{Kind: plan.EvidenceFile, Source: ctx.SourcePath(app)})
			return spec, true, nil
		}
	}
	return commandSpec{}, false, nil
}

func pytestEvidence(ctx provider.Context, project pythonProject) []plan.Evidence {
	var evidence []plan.Evidence
	for _, name := range []string{"pytest.ini", "pytest.toml", ".pytest.ini", "conftest.py"} {
		if fileExists(ctx.ProjectDir(), name) {
			evidence = append(evidence, plan.Evidence{Kind: plan.EvidenceConfiguration, Source: ctx.SourcePath(name)})
		}
	}
	if _, ok := project.ToolTables["pytest"]; ok {
		evidence = append(evidence, plan.Evidence{
			Kind:    plan.EvidenceConfiguration,
			Source:  ctx.SourcePath(project.Manifest),
			Pointer: "/tool/pytest",
		})
	}
	if dep, ok := project.Dependencies["pytest"]; ok {
		evidence = append(evidence, plan.Evidence{
			Kind:    plan.EvidenceDeclaration,
			Source:  ctx.SourcePath(depSourceFile(dep, project.Manifest)),
			Pointer: depPointer("pytest"),
		})
	}
	if dep, ok := project.Dependencies["pytest-django"]; ok {
		evidence = append(evidence, plan.Evidence{
			Kind:    plan.EvidenceDeclaration,
			Source:  ctx.SourcePath(depSourceFile(dep, project.Manifest)),
			Pointer: depPointer("pytest-django"),
		})
	}
	return evidence
}

func flaskApplicationFile(ctx provider.Context) string {
	for _, name := range []string{"app.py", "wsgi.py", "application.py"} {
		if fileExists(ctx.ProjectDir(), name) {
			return name
		}
	}
	return ""
}

func firstPythonTest(root string) (string, error) {
	path, err := firstFile(root, isPythonTestName)
	if err != nil {
		return "", fmt.Errorf("find Python tests: %w", err)
	}
	return path, nil
}

func firstManagePy(root string) (string, error) {
	path, err := firstFile(root, func(name string) bool { return name == "manage.py" })
	if err != nil {
		return "", fmt.Errorf("find manage.py: %w", err)
	}
	return path, nil
}

func isPythonTestName(name string) bool {
	if !strings.HasSuffix(name, ".py") {
		return false
	}
	return strings.HasPrefix(name, "test") || strings.HasSuffix(name, "_test.py")
}

func conventionSpec(source, name, run, pointer string, confidence plan.Confidence, description string) commandSpec {
	return commandSpec{
		name:       name,
		run:        run,
		pointer:    pointer,
		confidence: confidence,
		evidence: []plan.Evidence{
			{Kind: plan.EvidenceFile, Source: source},
			{Kind: plan.EvidenceConvention, Source: "python-ecosystem", Pointer: strings.TrimPrefix(pointer, "/#"), Description: description},
		},
		invocations: run,
	}
}

func addEvidenceAfterManifest(evidence []plan.Evidence, additions ...plan.Evidence) []plan.Evidence {
	combined := make([]plan.Evidence, 0, len(evidence)+len(additions))
	combined = append(combined, evidence[0])
	combined = append(combined, additions...)
	return append(combined, evidence[1:]...)
}

func lockfileFor(choice managerChoice, manager string) string {
	for _, signal := range choice.signals {
		if signal.name == manager && signal.lockfile != "" {
			return signal.lockfile
		}
	}
	return ""
}

func commandFromSpec(ctx provider.Context, spec commandSpec) (plan.Command, error) {
	source := spec.evidence[0].Source
	id, err := plan.NewCommandID(plan.CommandIdentity{
		ProjectPath: ctx.ProjectPath,
		Provider:    providerName,
		Source:      source,
		Pointer:     spec.pointer,
	})
	if err != nil {
		return plan.Command{}, err
	}
	return plan.Command{
		ID:              id,
		Name:            spec.name,
		Run:             stringPtr(spec.run),
		Directory:       ctx.ProjectPath,
		Scope:           plan.ScopeProject,
		Origin:          plan.CommandInferred,
		Confidence:      spec.confidence,
		Evidence:        spec.evidence,
		Interpretations: interpretations(spec),
		Variants:        []plan.CommandVariant{},
	}, nil
}

func interpretations(spec commandSpec) []plan.Interpretation {
	matches := knowledge.InterpretScript(spec.invocations)
	result := make([]plan.Interpretation, 0, len(matches))
	for _, match := range matches {
		result = append(result, plan.Interpretation{
			Capability: match.Capability,
			Confidence: match.Confidence,
			Evidence: []plan.Evidence{{
				Kind:        plan.EvidenceConvention,
				Source:      "python-ecosystem",
				Pointer:     strings.TrimPrefix(spec.pointer, "/#"),
				Description: match.Description,
			}},
		})
	}
	return result
}
