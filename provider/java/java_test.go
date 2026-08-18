package java

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

func TestDetectReturnsNothingWithoutManifest(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{"README.md": "hello\n"})
	if len(result.Findings) != 0 {
		t.Fatalf("Detect() = %+v, want no findings", result)
	}
}

func TestDetectMavenSpringBootWrapperRuntimeAndTests(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pom.xml": `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <parent>
    <groupId>org.springframework.boot</groupId>
    <artifactId>spring-boot-starter-parent</artifactId>
    <version>3.4.0</version>
    <relativePath/>
  </parent>
  <artifactId>demo</artifactId>
  <properties>
    <java.version>17</java.version>
  </properties>
  <dependencies>
    <dependency>
      <groupId>org.springframework.boot</groupId>
      <artifactId>spring-boot-starter-web</artifactId>
    </dependency>
  </dependencies>
  <build>
    <plugins>
      <plugin>
        <groupId>org.springframework.boot</groupId>
        <artifactId>spring-boot-maven-plugin</artifactId>
      </plugin>
      <plugin>
        <artifactId>maven-checkstyle-plugin</artifactId>
      </plugin>
    </plugins>
  </build>
</project>
`,
		"mvnw":                                  "#!/bin/sh\n",
		".mvn/wrapper/maven-wrapper.properties": "distributionUrl=https://repo.maven.apache.org/maven2/org/apache/maven/apache-maven/3.9.9/apache-maven-3.9.9-bin.zip\n",
		".java-version":                         "17\n",
		"checkstyle.xml":                        "<!DOCTYPE module>\n",
		"src/main/java/com/example/DemoApplication.java":     "public class DemoApplication { public static void main(String[] args) {} }\n",
		"src/test/java/com/example/DemoApplicationTest.java": "class DemoApplicationTest {}\n",
	})

	if !hasProperty(result, plan.PropertyLanguage, "java") {
		t.Fatalf("missing Java language in %+v", result.Findings)
	}
	if !hasProperty(result, plan.PropertyFramework, "spring-boot") {
		t.Fatalf("missing Spring Boot framework in %+v", result.Findings)
	}
	if !hasPackageManager(result, "maven", "3.9.9") {
		t.Fatalf("missing Maven 3.9.9 in %+v", result.Findings)
	}
	if !hasRuntime(result, "17") {
		t.Fatalf("missing Java 17 runtime in %+v", result.Findings)
	}

	commands := commandsByName(result)
	assertCommand(t, commands["test"], "./mvnw test", plan.CapabilityTestRun)
	assertCommand(t, commands["build"], "./mvnw package", plan.CapabilityArtifactBuild)
	assertCommand(t, commands["server"], "./mvnw spring-boot:run", plan.CapabilityApplicationRun)
	if !slices.Contains(factValues(result, "tool.configured"), "checkstyle") {
		t.Fatalf("configured tools = %v, want checkstyle", factValues(result, "tool.configured"))
	}
}

func TestDetectGradleKotlinDslWrapperAndBootRun(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"settings.gradle.kts": `rootProject.name = "demo"
include("lib")
`,
		"build.gradle.kts": `plugins {
    id("java")
    id("org.springframework.boot") version "3.4.0"
    id("com.diffplug.spotless") version "7.0.0"
}
java {
    toolchain {
        languageVersion = JavaLanguageVersion.of(21)
    }
}
`,
		"gradlew": "#!/bin/sh\n",
		"gradle/wrapper/gradle-wrapper.properties":       "distributionUrl=https\\://services.gradle.org/distributions/gradle-8.14-bin.zip\n",
		"src/main/java/com/example/DemoApplication.java": "public class DemoApplication {}\n",
		"lib/src/test/java/com/example/LibTest.java":     "class LibTest {}\n",
	})

	if !hasPackageManager(result, "gradle", "8.14") {
		t.Fatalf("missing Gradle 8.14 in %+v", result.Findings)
	}
	if !hasRuntime(result, "21") {
		t.Fatalf("missing Java 21 toolchain in %+v", result.Findings)
	}
	if !slices.Contains(factValues(result, "workspace.orchestrator"), "gradle") {
		t.Fatalf("missing gradle workspace orchestrator in %+v", result.Findings)
	}

	commands := commandsByName(result)
	assertCommand(t, commands["test"], "./gradlew test", plan.CapabilityTestRun)
	assertCommand(t, commands["build"], "./gradlew build", plan.CapabilityArtifactBuild)
	assertCommand(t, commands["server"], "./gradlew bootRun", plan.CapabilityApplicationRun)
	if !slices.Contains(factValues(result, "tool.configured"), "spotless") {
		t.Fatalf("configured tools = %v, want spotless", factValues(result, "tool.configured"))
	}
}

func TestDetectReportsCompetingMavenAndGradleAsAmbiguity(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pom.xml": `<project>
  <modelVersion>4.0.0</modelVersion>
  <artifactId>demo</artifactId>
  <properties><java.version>17</java.version></properties>
</project>
`,
		"build.gradle": `plugins { id 'java' }
java { toolchain { languageVersion = JavaLanguageVersion.of(17) } }
`,
		"mvnw":    "#!/bin/sh\n",
		"gradlew": "#!/bin/sh\n",
		"src/test/java/com/example/WidgetTest.java": "class WidgetTest {}\n",
	})

	if !hasPackageManager(result, "maven", "") || !hasPackageManager(result, "gradle", "") {
		t.Fatalf("missing both package managers in %+v", result.Findings)
	}
	if len(commandsByName(result)) != 0 {
		t.Fatalf("competing managers unexpectedly inferred commands: %+v", commandsByName(result))
	}
	subjects := ambiguitySubjects(result)
	if !slices.Contains(subjects, "test.run") || !slices.Contains(subjects, "artifact.build") {
		t.Fatalf("ambiguities = %v, want test.run and artifact.build", subjects)
	}
}

func TestDetectBareMavenWithoutWrapper(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pom.xml":                    `<project><modelVersion>4.0.0</modelVersion><artifactId>lib</artifactId></project>`,
		"src/test/java/LibTest.java": "class LibTest {}\n",
	})

	commands := commandsByName(result)
	assertCommand(t, commands["test"], "mvn test", plan.CapabilityTestRun)
	assertCommand(t, commands["build"], "mvn package", plan.CapabilityArtifactBuild)
	if _, ok := commands["server"]; ok {
		t.Fatal("non-Spring Maven project unexpectedly has a server command")
	}
}

func TestDetectMavenAggregatorReportsWorkspaceFact(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pom.xml": `<project>
  <modelVersion>4.0.0</modelVersion>
  <packaging>pom</packaging>
  <modules><module>lib</module></modules>
</project>
`,
		"lib/pom.xml": `<project><modelVersion>4.0.0</modelVersion></project>`,
	})
	if !slices.Contains(factValues(result, "workspace.orchestrator"), "maven") {
		t.Fatalf("missing maven workspace orchestrator in %+v", result.Findings)
	}
}

func TestDetectMavenChildInheritsParentCompilerRelease(t *testing.T) {
	t.Parallel()

	root := writeFiles(t, map[string]string{
		"pom.xml": `<project>
  <modelVersion>4.0.0</modelVersion>
  <artifactId>parent</artifactId>
  <packaging>pom</packaging>
  <modules><module>lib</module></modules>
  <properties><maven.compiler.release>8</maven.compiler.release></properties>
</project>
`,
		"lib/pom.xml": `<project>
  <modelVersion>4.0.0</modelVersion>
  <parent>
    <artifactId>parent</artifactId>
    <groupId>com.example</groupId>
    <version>1.0</version>
    <relativePath>../pom.xml</relativePath>
  </parent>
  <artifactId>lib</artifactId>
</project>
`,
	})

	result, err := Provider{}.Detect(provider.Context{RepositoryRoot: root, ProjectPath: "lib"})
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if !hasRuntime(result, "8") {
		t.Fatalf("missing inherited Java 8 in %+v", result.Findings)
	}
}

func TestDetectReportsConflictingRuntimePins(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pom.xml":        `<project><modelVersion>4.0.0</modelVersion></project>`,
		".java-version":  "17\n",
		".tool-versions": "java 21\n",
	})

	if len(result.Conflicts) != 1 || result.Conflicts[0].Subject != "runtime.java.version" {
		t.Fatalf("conflicts = %+v, want one Java runtime conflict", result.Conflicts)
	}
	if !hasRuntime(result, "17") || !hasRuntime(result, "21") {
		t.Fatalf("runtime findings = %+v, want both conflicting pins", result.Findings)
	}
}

func TestDetectMergesMatchingRuntimeEvidence(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pom.xml": `<project>
  <modelVersion>4.0.0</modelVersion>
  <properties><java.version>17</java.version></properties>
</project>
`,
		".java-version": "17\n",
	})

	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if !ok || item.Requirement.Name != "java" || item.Requirement.Version != "17" {
			continue
		}
		sources := make([]string, 0, len(item.Requirement.Evidence))
		for _, evidence := range item.Requirement.Evidence {
			sources = append(sources, evidence.Source)
		}
		if !slices.Contains(sources, ".java-version") || !slices.Contains(sources, "pom.xml") {
			t.Fatalf("runtime evidence = %v, want .java-version and pom.xml", sources)
		}
		return
	}
	t.Fatal("missing merged Java 17 requirement")
}

func TestDetectSpringBootWithoutMainSourcesDoesNotInferServer(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pom.xml": `<project>
  <modelVersion>4.0.0</modelVersion>
  <parent>
    <groupId>org.springframework.boot</groupId>
    <artifactId>spring-boot-starter-parent</artifactId>
    <version>3.4.0</version>
    <relativePath/>
  </parent>
</project>
`,
	})

	if _, ok := commandsByName(result)["server"]; ok {
		t.Fatal("Spring Boot without application sources unexpectedly has a server command")
	}
}

func detectFiles(t *testing.T, files map[string]string) provider.Result {
	t.Helper()

	result, err := Provider{}.Detect(provider.Context{RepositoryRoot: writeFiles(t, files), ProjectPath: "."})
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	return result
}

func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()

	root := t.TempDir()
	for name, contents := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("os.MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatalf("os.WriteFile() error = %v", err)
		}
	}
	return root
}

func hasProperty(result provider.Result, kind plan.PropertyKind, name string) bool {
	for _, finding := range result.Findings {
		item, ok := finding.(plan.PropertyFinding)
		if ok && item.Property.Kind == kind && item.Property.Name == name {
			return true
		}
	}
	return false
}

func hasPackageManager(result provider.Result, name, version string) bool {
	for _, finding := range result.Findings {
		item, ok := finding.(plan.PropertyFinding)
		if ok && item.Property.Kind == plan.PropertyPackageManager && item.Property.Name == name && item.Property.Version == version {
			return true
		}
	}
	return false
}

func hasRuntime(result provider.Result, version string) bool {
	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if ok && item.Requirement.Kind == plan.RequirementRuntime && item.Requirement.Name == "java" && item.Requirement.Version == version {
			return true
		}
	}
	return false
}

func commandsByName(result provider.Result) map[string]plan.Command {
	commands := make(map[string]plan.Command)
	for _, finding := range result.Findings {
		item, ok := finding.(plan.CommandFinding)
		if ok {
			commands[item.Command.Name] = item.Command
		}
	}
	return commands
}

func assertCommand(t *testing.T, command plan.Command, run string, capability plan.Capability) {
	t.Helper()
	if command.Run == nil || *command.Run != run || command.Origin != plan.CommandInferred {
		t.Fatalf("command = %+v, want inferred %q", command, run)
	}
	for _, interpretation := range command.Interpretations {
		if interpretation.Capability == capability {
			return
		}
	}
	t.Fatalf("command interpretations = %+v, want %s", command.Interpretations, capability)
}

func factValues(result provider.Result, name string) []string {
	var values []string
	for _, finding := range result.Findings {
		item, ok := finding.(plan.PropertyFinding)
		if ok && item.Property.Kind == plan.PropertyFact && item.Property.Name == name {
			values = append(values, item.Property.Value)
		}
	}
	return values
}

func ambiguitySubjects(result provider.Result) []string {
	subjects := make([]string, 0, len(result.Ambiguities))
	for _, item := range result.Ambiguities {
		subjects = append(subjects, item.Subject)
	}
	return subjects
}
