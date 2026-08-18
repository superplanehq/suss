package java

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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
	var specs []commandSpec
	var ambiguities []plan.Ambiguity
	var err error
	if project.competingManagers() {
		specs, ambiguities, err = competingManagerResult(ctx, project)
	} else {
		specs, err = inferredSpecs(ctx, project)
	}
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
	return findings, ambiguities, nil
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
	testFile, err := firstJavaTest(ctx.ProjectDir(), testSearch{
		match:        isSurefireTestName,
		mavenModules: maven.Modules,
	})
	if err != nil {
		return nil, err
	}

	var specs []commandSpec
	if testFile != "" {
		spec := conventionSpec(source, "test", tool+" test", "/#test", plan.ConfidenceHigh, "Maven projects conventionally run tests with mvn test.")
		spec.evidence = attachCommandEvidence(spec.evidence, maven.WrapperSource, plan.Evidence{Kind: plan.EvidenceFile, Source: ctx.SourcePath(testFile)})
		specs = append(specs, spec)
	}
	build := conventionSpec(source, "build", tool+" package", "/#build", plan.ConfidenceMedium, "Maven projects conventionally produce artifacts with mvn package.")
	build.evidence = attachCommandEvidence(build.evidence, maven.WrapperSource)
	specs = append(specs, build)
	entry, err := firstApplicationEntry(ctx.ProjectDir(), nil)
	if err != nil {
		return nil, err
	}
	if maven.hasSpringBootPlugin() && entry != "" {
		spec := conventionSpec(source, "server", tool+" spring-boot:run", "/#server", plan.ConfidenceMedium, "Spring Boot applications conventionally start with mvn spring-boot:run.")
		spec.evidence = attachCommandEvidence(spec.evidence, maven.WrapperSource, springBootPluginEvidence(maven), plan.Evidence{Kind: plan.EvidenceFile, Source: ctx.SourcePath(entry)})
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
	testFile, err := firstJavaTest(ctx.ProjectDir(), testSearch{
		match:          isGradleTestName,
		gradleMembers:  gradle.Members,
		requireTestDir: true,
	})
	if err != nil {
		return nil, err
	}

	var specs []commandSpec
	if testFile != "" {
		spec := conventionSpec(source, "test", tool+" test", "/#test", plan.ConfidenceHigh, "Gradle projects conventionally run tests with gradle test.")
		spec.evidence = attachCommandEvidence(spec.evidence, gradle.WrapperSource, plan.Evidence{Kind: plan.EvidenceFile, Source: ctx.SourcePath(testFile)})
		specs = append(specs, spec)
	}
	build := conventionSpec(source, "build", tool+" build", "/#build", plan.ConfidenceMedium, "Gradle projects conventionally produce artifacts with gradle build.")
	build.evidence = attachCommandEvidence(build.evidence, gradle.WrapperSource)
	specs = append(specs, build)
	servers, err := gradleServerSpecs(ctx, gradle)
	if err != nil {
		return nil, err
	}
	return append(specs, servers...), nil
}

func competingManagerResult(ctx provider.Context, project javaProject) ([]commandSpec, []plan.Ambiguity, error) {
	var modules, members []string
	if project.Maven != nil {
		modules = project.Maven.Modules
	}
	if project.Gradle != nil {
		members = project.Gradle.Members
	}
	mavenTest, err := firstJavaTest(ctx.ProjectDir(), testSearch{match: isSurefireTestName, mavenModules: modules})
	if err != nil {
		return nil, nil, err
	}
	gradleTest, err := firstJavaTest(ctx.ProjectDir(), testSearch{match: isGradleTestName, gradleMembers: members, requireTestDir: true})
	if err != nil {
		return nil, nil, err
	}
	entry, err := firstApplicationEntry(ctx.ProjectDir(), nil)
	if err != nil {
		return nil, nil, err
	}

	var ambiguities []plan.Ambiguity
	if mavenTest != "" || gradleTest != "" {
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

	var specs []commandSpec
	serverSpecs, serverAmbiguity, err := competingServerResult(ctx, project, entry)
	if err != nil {
		return nil, nil, err
	}
	specs = append(specs, serverSpecs...)
	if serverAmbiguity != nil {
		ambiguities = append(ambiguities, *serverAmbiguity)
	}
	return specs, ambiguities, nil
}

func competingServerResult(ctx provider.Context, project javaProject, entry string) ([]commandSpec, *plan.Ambiguity, error) {
	var specs []commandSpec
	if entry != "" && project.Maven != nil && project.Maven.hasSpringBootPlugin() {
		source := ctx.SourcePath(project.Maven.Source)
		tool := project.Maven.Wrapper
		if tool == "" {
			tool = "mvn"
		}
		spec := conventionSpec(source, "server", tool+" spring-boot:run", "/#server", plan.ConfidenceMedium, "Spring Boot applications conventionally start with mvn spring-boot:run.")
		spec.evidence = attachCommandEvidence(spec.evidence, project.Maven.WrapperSource, springBootPluginEvidence(project.Maven), plan.Evidence{Kind: plan.EvidenceFile, Source: ctx.SourcePath(entry)})
		specs = append(specs, spec)
	}
	var gradleSpecs []commandSpec
	if project.Gradle != nil {
		var err error
		gradleSpecs, err = gradleServerSpecs(ctx, project.Gradle)
		if err != nil {
			return nil, nil, err
		}
	}
	if len(specs) > 0 && len(gradleSpecs) > 0 {
		gradleRun := gradleSpecs[0].run
		ambiguity := managerAmbiguity(ctx, project, "application.run", "server", func(tool, kind string) string {
			if kind == "maven" {
				return tool + " spring-boot:run"
			}
			return gradleRun
		})
		return nil, &ambiguity, nil
	}
	return append(specs, gradleSpecs...), nil, nil
}

func gradleServerSpecs(ctx provider.Context, gradle *gradleProject) ([]commandSpec, error) {
	if gradle == nil {
		return nil, nil
	}
	tool := gradle.Wrapper
	if tool == "" {
		tool = "gradle"
	}
	var specs []commandSpec
	for _, build := range gradle.Builds {
		spec, err := build.serverSpec(ctx, gradle, tool)
		if err != nil {
			return nil, err
		}
		if spec.name != "" {
			specs = append(specs, spec)
		}
	}
	return specs, nil
}

func (b gradleBuild) serverSpec(ctx provider.Context, gradle *gradleProject, tool string) (commandSpec, error) {
	entry, err := b.applicationEntry(ctx)
	if err != nil {
		return commandSpec{}, err
	}
	pointer := serverPointer(b.Member)
	source := b.Source
	if source == "" {
		source = ctx.SourcePath(gradle.Source)
	}
	if b.hasSpringBootPlugin() && entry != "" {
		spec := conventionSpec(source, "server", tool+" "+gradleTaskPath(b.Member, "bootRun"), pointer, plan.ConfidenceMedium, "Spring Boot applications conventionally start with gradle bootRun.")
		spec.evidence = attachCommandEvidence(spec.evidence, gradle.WrapperSource, gradleBuildPluginEvidence(b, "org.springframework.boot"), plan.Evidence{Kind: plan.EvidenceFile, Source: ctx.SourcePath(entry)})
		return spec, nil
	}
	if b.canRunApplication() {
		spec := conventionSpec(source, "server", tool+" "+gradleTaskPath(b.Member, "run"), pointer, plan.ConfidenceMedium, "Gradle application projects conventionally start with gradle run.")
		extras := []plan.Evidence{gradleBuildMainClassEvidence(b)}
		if entry != "" {
			extras = append(extras, plan.Evidence{Kind: plan.EvidenceFile, Source: ctx.SourcePath(entry)})
		}
		spec.evidence = attachCommandEvidence(spec.evidence, gradle.WrapperSource, extras...)
		return spec, nil
	}
	return commandSpec{}, nil
}

func (b gradleBuild) applicationEntry(ctx provider.Context) (string, error) {
	start := filepath.Join(ctx.ProjectDir(), "src", "main")
	if b.Member != "" {
		dir, ok := repoMemberDir(ctx, b.Member)
		if !ok {
			return "", nil
		}
		start = filepath.Join(dir, "src", "main")
	}
	return walkApplicationEntry(ctx.ProjectDir(), start)
}

func gradleTaskPath(member, task string) string {
	if member == "" {
		return task
	}
	return ":" + strings.ReplaceAll(filepath.ToSlash(member), "/", ":") + ":" + task
}

func serverPointer(member string) string {
	if member == "" {
		return "/#server"
	}
	return "/#server/" + member
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

func firstApplicationEntry(root string, members []string) (string, error) {
	starts := []string{filepath.Join(root, "src", "main")}
	for _, member := range members {
		starts = append(starts, filepath.Join(root, filepath.FromSlash(member), "src", "main"))
	}
	for _, start := range starts {
		found, err := walkApplicationEntry(root, start)
		if err != nil || found != "" {
			return found, err
		}
	}
	return "", nil
}

func walkApplicationEntry(root, start string) (string, error) {
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
			if skipJavaWalkDir(entry.Name()) || entry.Type()&os.ModeSymlink != 0 {
				if path != start {
					return filepath.SkipDir
				}
				return nil
			}
			return nil
		}
		if !isJvmSourceName(entry.Name()) {
			return nil
		}
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if !hasApplicationEntry(string(contents)) {
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
		return "", fmt.Errorf("find Java application entry: %w", err)
	}
	return first, nil
}

func isJvmSourceName(name string) bool {
	return strings.HasSuffix(name, ".java") || strings.HasSuffix(name, ".kt")
}

var (
	javaMainPattern              = regexp.MustCompile(`public\s+static\s+void\s+main\s*\(`)
	kotlinMainPattern            = regexp.MustCompile(`(?m)fun\s+main\s*\(`)
	springBootApplicationPattern = regexp.MustCompile(`@SpringBootApplication\b`)
)

func hasApplicationEntry(contents string) bool {
	return springBootApplicationPattern.MatchString(contents) || javaMainPattern.MatchString(contents) || kotlinMainPattern.MatchString(contents)
}

func firstJavaTest(root string, search testSearch) (string, error) {
	if found, err := walkJavaTests(root, filepath.Join(root, "src", "test"), search); err != nil || found != "" {
		return found, err
	}
	return walkJavaTests(root, root, search)
}

type testSearch struct {
	match          func(string) bool
	mavenModules   []string
	gradleMembers  []string
	requireTestDir bool
}

func walkJavaTests(root, start string, search testSearch) (string, error) {
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
			if isJavaMainSourceDir(path) {
				return filepath.SkipDir
			}
			if path != root && skipNestedTestTree(root, path, search) {
				return filepath.SkipDir
			}
			if path != root && (fileExists(path, "settings.gradle") || fileExists(path, "settings.gradle.kts")) {
				return filepath.SkipDir
			}
			return nil
		}
		if search.match == nil || !search.match(entry.Name()) {
			return nil
		}
		if search.requireTestDir && !isUnderSrcTest(path) {
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

func skipNestedTestTree(root, path string, search testSearch) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return true
	}
	relative := filepath.ToSlash(rel)
	declared := underDeclaredMember(relative, search.mavenModules) || underDeclaredMember(relative, search.gradleMembers)
	if fileExists(path, "pom.xml") && !declared {
		return true
	}
	if (fileExists(path, "build.gradle") || fileExists(path, "build.gradle.kts")) && !declared {
		return true
	}
	return false
}

func underDeclaredMember(relative string, members []string) bool {
	for _, member := range members {
		if relative == member || strings.HasPrefix(relative, member+"/") {
			return true
		}
	}
	return false
}

func springBootPluginEvidence(maven *mavenProject) plan.Evidence {
	return plan.Evidence{
		Kind:    plan.EvidenceDeclaration,
		Source:  maven.pluginSource("spring-boot-maven-plugin"),
		Pointer: "/build/plugins/spring-boot-maven-plugin",
	}
}

func gradleBuildPluginEvidence(build gradleBuild, plugin string) plan.Evidence {
	return plan.Evidence{
		Kind:    plan.EvidenceDeclaration,
		Source:  build.Source,
		Pointer: "/plugins/" + pointerToken(plugin),
	}
}

func gradleBuildMainClassEvidence(build gradleBuild) plan.Evidence {
	return plan.Evidence{
		Kind:    plan.EvidenceDeclaration,
		Source:  build.Source,
		Pointer: build.MainClassPointer,
	}
}

func attachCommandEvidence(evidence []plan.Evidence, wrapperSource string, extras ...plan.Evidence) []plan.Evidence {
	additions := extras
	if wrapperSource != "" {
		additions = append([]plan.Evidence{{Kind: plan.EvidenceFile, Source: wrapperSource}}, extras...)
	}
	if len(additions) == 0 {
		return evidence
	}
	return addEvidenceAfterManifest(evidence, additions...)
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

func isJavaMainSourceDir(path string) bool {
	return filepath.Base(path) == "main" && filepath.Base(filepath.Dir(path)) == "src"
}

func isSurefireTestName(name string) bool {
	switch {
	case strings.HasSuffix(name, "Test.java"), strings.HasSuffix(name, "Tests.java"), strings.HasSuffix(name, "TestCase.java"):
		return true
	case strings.HasSuffix(name, "Test.kt"), strings.HasSuffix(name, "Tests.kt"), strings.HasSuffix(name, "TestCase.kt"):
		return true
	case strings.HasPrefix(name, "Test") && (strings.HasSuffix(name, ".java") || strings.HasSuffix(name, ".kt")):
		return true
	default:
		return false
	}
}

func isGradleTestName(name string) bool {
	return isJvmSourceName(name)
}

func isUnderSrcTest(path string) bool {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i := 0; i < len(parts)-1; i++ {
		if parts[i] == "src" && parts[i+1] == "test" {
			return true
		}
	}
	return false
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
