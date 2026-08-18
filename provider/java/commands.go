package java

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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

func commandFindings(ctx provider.Context, project javaProject) ([]plan.Finding, []plan.Ambiguity, error) {
	if project.competingManagers() {
		ambiguities, err := competingManagerAmbiguities(ctx, project)
		return nil, ambiguities, err
	}

	specs, err := inferredSpecs(ctx, project)
	if err != nil {
		return nil, nil, err
	}
	findings := make([]plan.Finding, 0, len(specs))
	for _, spec := range specs {
		command, err := commandFromSpec(ctx, spec)
		if err != nil {
			return nil, nil, err
		}
		findings = append(findings, plan.CommandFinding{ProjectPath: ctx.ProjectPath, Detector: providerName, Command: command})
	}
	return findings, nil, nil
}

func inferredSpecs(ctx provider.Context, project javaProject) ([]commandSpec, error) {
	if project.Maven != nil {
		return mavenSpecs(ctx, project.Maven)
	}
	if project.Gradle != nil {
		return gradleSpecs(ctx, project.Gradle)
	}
	return nil, nil
}

func mavenSpecs(ctx provider.Context, maven *mavenProject) ([]commandSpec, error) {
	source := ctx.SourcePath(maven.Source)
	tool := maven.Wrapper
	if tool == "" {
		tool = "mvn"
	}
	includeNested := maven.Aggregator
	testFile, err := firstJavaTest(ctx.ProjectDir(), includeNested)
	if err != nil {
		return nil, err
	}

	var specs []commandSpec
	if testFile != "" {
		spec := conventionSpec(source, "test", tool+" test", "/#test", plan.ConfidenceHigh, "Maven projects conventionally run tests with mvn test.")
		spec.evidence = addEvidenceAfterManifest(spec.evidence, plan.Evidence{Kind: plan.EvidenceFile, Source: ctx.SourcePath(testFile)})
		specs = append(specs, spec)
	}
	specs = append(specs, conventionSpec(source, "build", tool+" package", "/#build", plan.ConfidenceMedium, "Maven projects conventionally produce artifacts with mvn package."))
	if maven.SpringBoot && springApplicationEvidence(ctx) {
		spec := conventionSpec(source, "server", tool+" spring-boot:run", "/#server", plan.ConfidenceMedium, "Spring Boot applications conventionally start with mvn spring-boot:run.")
		specs = append(specs, spec)
	}
	return specs, nil
}

func gradleSpecs(ctx provider.Context, gradle *gradleProject) ([]commandSpec, error) {
	source := ctx.SourcePath(gradle.Source)
	tool := gradle.Wrapper
	if tool == "" {
		tool = "gradle"
	}
	testFile, err := firstJavaTest(ctx.ProjectDir(), true)
	if err != nil {
		return nil, err
	}

	var specs []commandSpec
	if testFile != "" {
		spec := conventionSpec(source, "test", tool+" test", "/#test", plan.ConfidenceHigh, "Gradle projects conventionally run tests with gradle test.")
		spec.evidence = addEvidenceAfterManifest(spec.evidence, plan.Evidence{Kind: plan.EvidenceFile, Source: ctx.SourcePath(testFile)})
		specs = append(specs, spec)
	}
	specs = append(specs, conventionSpec(source, "build", tool+" build", "/#build", plan.ConfidenceMedium, "Gradle projects conventionally produce artifacts with gradle build."))
	if gradle.SpringBoot && springApplicationEvidence(ctx) {
		specs = append(specs, conventionSpec(source, "server", tool+" bootRun", "/#server", plan.ConfidenceMedium, "Spring Boot applications conventionally start with gradle bootRun."))
	} else if gradle.ApplicationPlugin {
		specs = append(specs, conventionSpec(source, "server", tool+" run", "/#server", plan.ConfidenceMedium, "Gradle application projects conventionally start with gradle run."))
	}
	return specs, nil
}

func competingManagerAmbiguities(ctx provider.Context, project javaProject) ([]plan.Ambiguity, error) {
	testFile, err := firstJavaTest(ctx.ProjectDir(), true)
	if err != nil {
		return nil, err
	}
	spring := len(springBootEvidence(ctx, project)) > 0 && springApplicationEvidence(ctx)

	var ambiguities []plan.Ambiguity
	if testFile != "" {
		ambiguities = append(ambiguities, managerAmbiguity(ctx, project, "test.run", "test", func(tool, _ string) string {
			return tool + " test"
		}))
	}
	ambiguities = append(ambiguities, managerAmbiguity(ctx, project, "artifact.build", "build", func(tool, kind string) string {
		if kind == "maven" {
			return tool + " package"
		}
		return tool + " build"
	}))
	if spring {
		ambiguities = append(ambiguities, managerAmbiguity(ctx, project, "application.run", "server", func(tool, kind string) string {
			if kind == "maven" {
				return tool + " spring-boot:run"
			}
			return tool + " bootRun"
		}))
	}
	return ambiguities, nil
}

func managerAmbiguity(ctx provider.Context, project javaProject, subject, name string, run func(tool, kind string) string) plan.Ambiguity {
	var candidates []plan.Candidate
	if project.Maven != nil {
		tool := project.Maven.Wrapper
		if tool == "" {
			tool = "mvn"
		}
		candidates = append(candidates, plan.Candidate{
			Value:    run(tool, "maven"),
			Evidence: mavenManagerEvidence(ctx, project.Maven),
		})
	}
	if project.Gradle != nil {
		tool := project.Gradle.Wrapper
		if tool == "" {
			tool = "gradle"
		}
		candidates = append(candidates, plan.Candidate{
			Value:    run(tool, "gradle"),
			Evidence: gradleManagerEvidence(ctx, project.Gradle),
		})
	}
	return plan.Ambiguity{
		Subject:    subject,
		Message:    "Maven and Gradle manifests are both present; the " + name + " command cannot be selected.",
		Candidates: candidates,
	}
}

func springApplicationEvidence(ctx provider.Context) bool {
	return dirExists(ctx.ProjectDir(), "src/main") || dirExists(ctx.ProjectDir(), "src/main/java")
}

func conventionSpec(source, name, run, pointer string, confidence plan.Confidence, description string) commandSpec {
	return commandSpec{
		name:       name,
		run:        run,
		pointer:    pointer,
		confidence: confidence,
		evidence: []plan.Evidence{
			{Kind: plan.EvidenceFile, Source: source},
			{Kind: plan.EvidenceConvention, Source: "java-ecosystem", Pointer: strings.TrimPrefix(pointer, "/#"), Description: description},
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

func firstJavaTest(root string, includeNestedMaven bool) (string, error) {
	if found, err := walkJavaTests(root, filepath.Join(root, "src", "test"), includeNestedMaven); err != nil || found != "" {
		return found, err
	}
	return walkJavaTests(root, root, includeNestedMaven)
}

func walkJavaTests(root, start string, includeNestedMaven bool) (string, error) {
	var first string
	err := filepath.WalkDir(start, func(path string, entry fs.DirEntry, err error) error {
		if os.IsNotExist(err) && path == start {
			return fs.SkipAll
		}
		if err != nil {
			return err
		}
		if first != "" {
			return fs.SkipAll
		}
		if entry.IsDir() {
			if path == start {
				return nil
			}
			if skipJavaWalkDir(entry.Name()) || entry.Type()&os.ModeSymlink != 0 {
				return filepath.SkipDir
			}
			if path != root && !includeNestedMaven && fileExists(path, "pom.xml") {
				return filepath.SkipDir
			}
			if path != root && (fileExists(path, "settings.gradle") || fileExists(path, "settings.gradle.kts")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !isJavaTestName(entry.Name()) {
			return nil
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		first = filepath.ToSlash(relative)
		return fs.SkipAll
	})
	if err != nil {
		return "", fmt.Errorf("find Java tests: %w", err)
	}
	return first, nil
}

func skipJavaWalkDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "vendor", "node_modules", "_build", "deps", "dist", "target", "tmp", "build", "buildSrc":
		return true
	default:
		return false
	}
}

func isJavaTestName(name string) bool {
	switch {
	case strings.HasSuffix(name, "Test.java"), strings.HasSuffix(name, "Tests.java"), strings.HasSuffix(name, "IT.java"):
		return true
	case strings.HasSuffix(name, "Test.kt"), strings.HasSuffix(name, "Tests.kt"):
		return true
	default:
		return false
	}
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
				Source:      "java-ecosystem",
				Pointer:     strings.TrimPrefix(spec.pointer, "/#"),
				Description: match.Description,
			}},
		})
	}
	return result
}
