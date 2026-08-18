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
		"src/main/java/com/example/DemoApplication.java": "public class DemoApplication { public static void main(String[] args) {} }\n",
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
  <groupId>com.example</groupId>
  <artifactId>parent</artifactId>
  <version>1.0</version>
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
	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if !ok || item.Requirement.Name != "java" || item.Requirement.Version != "8" {
			continue
		}
		for _, evidence := range item.Requirement.Evidence {
			if evidence.Source != "pom.xml" {
				t.Fatalf("inherited Java 8 evidence source = %q, want parent pom.xml", evidence.Source)
			}
			if evidence.Pointer != "/properties/maven.compiler.release" {
				t.Fatalf("inherited Java 8 pointer = %q, want /properties/maven.compiler.release", evidence.Pointer)
			}
		}
		return
	}
	t.Fatal("missing inherited Java 8 requirement")
}

func TestDetectMavenChildCompilerPluginOverridesParent(t *testing.T) {
	t.Parallel()

	root := writeFiles(t, map[string]string{
		"pom.xml": `<project>
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>parent</artifactId>
  <version>1.0</version>
  <packaging>pom</packaging>
  <modules><module>lib</module></modules>
  <build>
    <plugins>
      <plugin>
        <artifactId>maven-compiler-plugin</artifactId>
        <configuration><release>8</release></configuration>
      </plugin>
    </plugins>
  </build>
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
  <build>
    <plugins>
      <plugin>
        <artifactId>maven-compiler-plugin</artifactId>
        <configuration><release>17</release></configuration>
      </plugin>
    </plugins>
  </build>
</project>
`,
	})

	result, err := Provider{}.Detect(provider.Context{RepositoryRoot: root, ProjectPath: "lib"})
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if !hasRuntime(result, "17") {
		t.Fatalf("missing child Java 17 in %+v", result.Findings)
	}
	if hasRuntime(result, "8") {
		t.Fatalf("parent compiler plugin release leaked into child: %+v", result.Findings)
	}
}

func TestDetectIgnoresNonInheritedParentPlugins(t *testing.T) {
	t.Parallel()

	root := writeFiles(t, map[string]string{
		"pom.xml": `<project>
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>parent</artifactId>
  <version>1.0</version>
  <packaging>pom</packaging>
  <modules><module>lib</module></modules>
  <build>
    <plugins>
      <plugin>
        <artifactId>maven-compiler-plugin</artifactId>
        <inherited>false</inherited>
        <configuration><release>8</release></configuration>
      </plugin>
      <plugin>
        <artifactId>maven-checkstyle-plugin</artifactId>
        <inherited>false</inherited>
      </plugin>
      <plugin>
        <groupId>org.springframework.boot</groupId>
        <artifactId>spring-boot-maven-plugin</artifactId>
        <inherited>false</inherited>
      </plugin>
    </plugins>
  </build>
</project>
`,
		"lib/pom.xml": `<project>
  <modelVersion>4.0.0</modelVersion>
  <parent>
    <groupId>com.example</groupId>
    <artifactId>parent</artifactId>
    <version>1.0</version>
    <relativePath>../pom.xml</relativePath>
  </parent>
  <artifactId>lib</artifactId>
  <dependencies>
    <dependency>
      <groupId>org.springframework.boot</groupId>
      <artifactId>spring-boot-starter-web</artifactId>
    </dependency>
  </dependencies>
</project>
`,
		"lib/src/main/java/com/example/App.java": "public class App { public static void main(String[] args) {} }\n",
	})

	result, err := Provider{}.Detect(provider.Context{RepositoryRoot: root, ProjectPath: "lib"})
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if hasRuntime(result, "8") {
		t.Fatalf("inherited=false compiler plugin supplied Java 8: %+v", result.Findings)
	}
	if slices.Contains(factValues(result, "tool.configured"), "checkstyle") {
		t.Fatal("inherited=false checkstyle plugin was reported as configured")
	}
	if _, ok := commandsByName(result)["server"]; ok {
		t.Fatal("inferred spring-boot:run from inherited=false spring-boot-maven-plugin")
	}
}

func TestDetectMavenChildKeepsParentCompilerReleaseWhenRedeclaring(t *testing.T) {
	t.Parallel()

	root := writeFiles(t, map[string]string{
		"pom.xml": `<project>
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>parent</artifactId>
  <version>1.0</version>
  <packaging>pom</packaging>
  <modules><module>lib</module></modules>
  <build>
    <plugins>
      <plugin>
        <artifactId>maven-compiler-plugin</artifactId>
        <configuration><release>17</release></configuration>
      </plugin>
    </plugins>
  </build>
</project>
`,
		"lib/pom.xml": `<project>
  <modelVersion>4.0.0</modelVersion>
  <parent>
    <groupId>com.example</groupId>
    <artifactId>parent</artifactId>
    <version>1.0</version>
    <relativePath>../pom.xml</relativePath>
  </parent>
  <artifactId>lib</artifactId>
  <build>
    <plugins>
      <plugin>
        <artifactId>maven-compiler-plugin</artifactId>
        <configuration>
          <compilerArgs>
            <arg>-parameters</arg>
          </compilerArgs>
        </configuration>
      </plugin>
    </plugins>
  </build>
</project>
`,
	})

	result, err := Provider{}.Detect(provider.Context{RepositoryRoot: root, ProjectPath: "lib"})
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if !hasRuntime(result, "17") {
		t.Fatalf("missing merged parent Java 17 in %+v", result.Findings)
	}
	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if !ok || item.Requirement.Name != "java" || item.Requirement.Version != "17" {
			continue
		}
		for _, evidence := range item.Requirement.Evidence {
			if evidence.Source != "pom.xml" {
				t.Fatalf("merged compiler release evidence source = %q, want parent pom.xml", evidence.Source)
			}
			if evidence.Pointer != "/build/plugins/maven-compiler-plugin/configuration/release" {
				t.Fatalf("merged compiler release pointer = %q", evidence.Pointer)
			}
		}
		return
	}
	t.Fatal("missing merged Java 17 requirement")
}

func TestDetectMavenModuleUsesAncestorWrapper(t *testing.T) {
	t.Parallel()

	root := writeFiles(t, map[string]string{
		"pom.xml": `<project>
  <modelVersion>4.0.0</modelVersion>
  <packaging>pom</packaging>
  <modules><module>lib</module></modules>
</project>
`,
		"mvnw":                                  "#!/bin/sh\n",
		".mvn/wrapper/maven-wrapper.properties": "distributionUrl=https://repo.maven.apache.org/maven2/org/apache/maven/apache-maven/3.9.9/apache-maven-3.9.9-bin.zip\n",
		"lib/pom.xml":                           `<project><modelVersion>4.0.0</modelVersion><artifactId>lib</artifactId></project>`,
		"lib/src/test/java/LibTest.java":        "class LibTest {}\n",
	})

	result, err := Provider{}.Detect(provider.Context{RepositoryRoot: root, ProjectPath: "lib"})
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if !hasPackageManager(result, "maven", "3.9.9") {
		t.Fatalf("missing ancestor Maven wrapper version in %+v", result.Findings)
	}
	assertCommand(t, commandsByName(result)["test"], "../mvnw test", plan.CapabilityTestRun)
	for _, finding := range result.Findings {
		item, ok := finding.(plan.PropertyFinding)
		if !ok || item.Property.Kind != plan.PropertyPackageManager || item.Property.Name != "maven" {
			continue
		}
		var sources []string
		for _, evidence := range item.Property.Evidence {
			sources = append(sources, evidence.Source)
		}
		if !slices.Contains(sources, ".mvn/wrapper/maven-wrapper.properties") {
			t.Fatalf("wrapper evidence = %v, want repo-root maven-wrapper.properties", sources)
		}
		if slices.Contains(sources, "lib/.mvn/wrapper/maven-wrapper.properties") {
			t.Fatalf("wrapper evidence fabricated a module-local properties path: %v", sources)
		}
		return
	}
	t.Fatal("missing Maven package manager finding")
}

func TestDetectIgnoresMavenPluginManagementSpringBoot(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pom.xml": `<project>
  <modelVersion>4.0.0</modelVersion>
  <artifactId>lib</artifactId>
  <build>
    <pluginManagement>
      <plugins>
        <plugin>
          <groupId>org.springframework.boot</groupId>
          <artifactId>spring-boot-maven-plugin</artifactId>
        </plugin>
      </plugins>
    </pluginManagement>
  </build>
</project>
`,
		"src/main/java/com/example/App.java": "public class App { public static void main(String[] args) {} }\n",
	})

	if hasProperty(result, plan.PropertyFramework, "spring-boot") {
		t.Fatal("pluginManagement-only spring-boot-maven-plugin was treated as Spring Boot")
	}
	if _, ok := commandsByName(result)["server"]; ok {
		t.Fatal("inferred spring-boot:run from pluginManagement")
	}
}

func TestDetectAggregatesIncludedGradleMemberMetadata(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"settings.gradle.kts": "rootProject.name = \"demo\"\ninclude(\"app\")\n",
		"build.gradle.kts":    "plugins { id(\"java\") }\n",
		"app/build.gradle.kts": `plugins {
    id("java")
    id("org.springframework.boot") version "3.4.0"
    id("checkstyle")
}
java { toolchain { languageVersion = JavaLanguageVersion.of(21) } }
`,
		"app/src/main/java/com/example/DemoApplication.java": "public class DemoApplication { public static void main(String[] args) {} }\n",
	})

	if !hasProperty(result, plan.PropertyFramework, "spring-boot") {
		t.Fatalf("missing member Spring Boot in %+v", result.Findings)
	}
	if !hasRuntime(result, "21") {
		t.Fatalf("missing member Java 21 toolchain in %+v", result.Findings)
	}
	if !slices.Contains(factValues(result, "tool.configured"), "checkstyle") {
		t.Fatalf("missing member checkstyle in %+v", result.Findings)
	}
	assertCommand(t, commandsByName(result)["server"], "gradle bootRun", plan.CapabilityApplicationRun)

	for _, finding := range result.Findings {
		switch item := finding.(type) {
		case plan.PropertyFinding:
			if item.Property.Kind == plan.PropertyFramework && item.Property.Name == "spring-boot" {
				if !slices.Contains(evidenceSources(item.Property.Evidence), "app/build.gradle.kts") {
					t.Fatalf("Spring Boot evidence = %v, want app/build.gradle.kts", evidenceSources(item.Property.Evidence))
				}
			}
			if item.Property.Kind == plan.PropertyFact && item.Property.Name == "tool.configured" && item.Property.Value == "checkstyle" {
				if !slices.Contains(evidenceSources(item.Property.Evidence), "app/build.gradle.kts") {
					t.Fatalf("checkstyle evidence = %v, want app/build.gradle.kts", evidenceSources(item.Property.Evidence))
				}
			}
		case plan.RequirementFinding:
			if item.Requirement.Name == "java" && item.Requirement.Version == "21" {
				if !slices.Contains(evidenceSources(item.Requirement.Evidence), "app/build.gradle.kts") {
					t.Fatalf("Java 21 evidence = %v, want app/build.gradle.kts", evidenceSources(item.Requirement.Evidence))
				}
			}
		case plan.CommandFinding:
			if item.Command.Name == "server" {
				if !slices.Contains(evidenceSources(item.Command.Evidence), "app/build.gradle.kts") {
					t.Fatalf("server evidence = %v, want app/build.gradle.kts", evidenceSources(item.Command.Evidence))
				}
			}
		}
	}
}

func TestDetectGradleLegacyJavaVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
	}{
		{name: "enum", src: "plugins { id 'java' }\nsourceCompatibility = JavaVersion.VERSION_1_8\n"},
		{name: "quoted", src: "plugins { id 'java' }\nsourceCompatibility = '1.8'\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := detectFiles(t, map[string]string{"build.gradle": tt.src})
			if !hasRuntime(result, "8") {
				t.Fatalf("missing normalized Java 8 in %+v", result.Findings)
			}
			if hasRuntime(result, "1") {
				t.Fatalf("legacy Java 8 declaration was reported as Java 1: %+v", result.Findings)
			}
		})
	}
}

func TestDetectIgnoresUnappliedGradlePlugins(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"settings.gradle.kts": `pluginManagement {
    plugins {
        id("org.springframework.boot") version "3.4.0"
    }
}
rootProject.name = "demo"
`,
		"build.gradle.kts": `plugins {
    id("java")
    id("org.springframework.boot") version "3.4.0" apply false
    id("com.diffplug.spotless") version "7.0.0" apply false
}
`,
		"src/main/java/com/example/App.java":     "public class App {}\n",
		"src/test/java/com/example/AppTest.java": "class AppTest {}\n",
	})

	if hasProperty(result, plan.PropertyFramework, "spring-boot") {
		t.Fatal("unapplied Spring Boot plugin was treated as applied")
	}
	if _, ok := commandsByName(result)["server"]; ok {
		t.Fatal("inferred bootRun from unapplied plugin")
	}
	if slices.Contains(factValues(result, "tool.configured"), "spotless") {
		t.Fatal("unapplied Spotless plugin was reported as configured")
	}
}

func TestDetectMavenInfersTestsFromSurefireDefaultNames(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pom.xml":                           `<project><modelVersion>4.0.0</modelVersion><artifactId>lib</artifactId></project>`,
		"src/test/java/TestWidget.java":     "class TestWidget {}\n",
		"src/test/java/WidgetTestCase.java": "class WidgetTestCase {}\n",
	})

	assertCommand(t, commandsByName(result)["test"], "mvn test", plan.CapabilityTestRun)
}

func TestDetectDoesNotTreatMainSourcesAsTests(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pom.xml": `<project><modelVersion>4.0.0</modelVersion><artifactId>lib</artifactId></project>`,
		"src/main/java/org/example/TestFinishedEvent.java": "class TestFinishedEvent {}\n",
	})

	if _, ok := commandsByName(result)["test"]; ok {
		t.Fatal("main-source Test*.java was treated as a test file")
	}
}

func TestDetectMavenDoesNotInferTestFromFailsafeIT(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pom.xml":                     `<project><modelVersion>4.0.0</modelVersion><artifactId>lib</artifactId></project>`,
		"src/test/java/WidgetIT.java": "class WidgetIT {}\n",
	})

	if _, ok := commandsByName(result)["test"]; ok {
		t.Fatal("inferred mvn test from Failsafe *IT.java")
	}
}

func TestDetectGradleDoesNotUseNestedMavenTests(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"settings.gradle.kts":                     "rootProject.name = \"demo\"\ninclude(\"lib\")\n",
		"build.gradle.kts":                        "plugins { id(\"java\") }\n",
		"lib/build.gradle.kts":                    "plugins { id(\"java\") }\n",
		"services/api/pom.xml":                    `<project><modelVersion>4.0.0</modelVersion><artifactId>api</artifactId></project>`,
		"services/api/src/test/java/ApiTest.java": "class ApiTest {}\n",
	})

	if _, ok := commandsByName(result)["test"]; ok {
		t.Fatal("inferred gradle test from an unrelated nested Maven project")
	}
}

func TestDetectMavenAggregatorDoesNotUseUnrelatedNestedTests(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pom.xml": `<project>
  <modelVersion>4.0.0</modelVersion>
  <packaging>pom</packaging>
  <modules><module>lib</module></modules>
</project>
`,
		"lib/pom.xml":                        `<project><modelVersion>4.0.0</modelVersion><artifactId>lib</artifactId></project>`,
		"other/pom.xml":                      `<project><modelVersion>4.0.0</modelVersion><artifactId>other</artifactId></project>`,
		"other/src/test/java/OtherTest.java": "class OtherTest {}\n",
	})

	if _, ok := commandsByName(result)["test"]; ok {
		t.Fatal("inferred mvn test from an unrelated nested Maven project")
	}
}

func TestDetectMavenAggregatorUsesDeclaredModuleTests(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pom.xml": `<project>
  <modelVersion>4.0.0</modelVersion>
  <packaging>pom</packaging>
  <modules><module>lib</module></modules>
</project>
`,
		"lib/pom.xml":                        `<project><modelVersion>4.0.0</modelVersion><artifactId>lib</artifactId></project>`,
		"lib/build.gradle":                   "plugins { id 'java' }\n",
		"lib/src/test/java/LibTest.java":     "class LibTest {}\n",
		"other/pom.xml":                      `<project><modelVersion>4.0.0</modelVersion><artifactId>other</artifactId></project>`,
		"other/src/test/java/OtherTest.java": "class OtherTest {}\n",
	})

	assertCommand(t, commandsByName(result)["test"], "mvn test", plan.CapabilityTestRun)
}

func TestDetectGradleInfersTestFromNonSurefireName(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"build.gradle": "plugins { id 'java' }\n",
		"src/test/java/com/example/WidgetSpec.java": "class WidgetSpec {}\n",
	})

	assertCommand(t, commandsByName(result)["test"], "gradle test", plan.CapabilityTestRun)
}

func TestDetectGradleDoesNotUseUnlistedNestedGradleTests(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"settings.gradle.kts":                  "rootProject.name = \"demo\"\ninclude(\"lib\")\n",
		"build.gradle.kts":                     "plugins { id(\"java\") }\n",
		"lib/build.gradle.kts":                 "plugins { id(\"java\") }\n",
		"orphan/build.gradle":                  "plugins { id 'java' }\n",
		"orphan/src/test/java/OrphanTest.java": "class OrphanTest {}\n",
	})

	if _, ok := commandsByName(result)["test"]; ok {
		t.Fatal("inferred gradle test from an unlisted nested Gradle build")
	}
}

func TestDetectDoesNotInheritUnrelatedNeighborPOM(t *testing.T) {
	t.Parallel()

	root := writeFiles(t, map[string]string{
		"pom.xml": `<project>
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>unrelated</artifactId>
  <version>1.0</version>
  <properties><maven.compiler.release>8</maven.compiler.release></properties>
</project>
`,
		"app/pom.xml": `<project>
  <modelVersion>4.0.0</modelVersion>
  <parent>
    <groupId>org.springframework.boot</groupId>
    <artifactId>spring-boot-starter-parent</artifactId>
    <version>3.4.0</version>
  </parent>
  <artifactId>app</artifactId>
</project>
`,
	})

	result, err := Provider{}.Detect(provider.Context{RepositoryRoot: root, ProjectPath: "app"})
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if hasRuntime(result, "8") {
		t.Fatalf("unrelated neighbor POM supplied Java 8: %+v", result.Findings)
	}
}

func TestDetectDoesNotInheritNeighborWithMismatchedParentGroup(t *testing.T) {
	t.Parallel()

	root := writeFiles(t, map[string]string{
		"pom.xml": `<project>
  <modelVersion>4.0.0</modelVersion>
  <artifactId>parent</artifactId>
  <version>1.0</version>
  <properties><maven.compiler.release>8</maven.compiler.release></properties>
</project>
`,
		"app/pom.xml": `<project>
  <modelVersion>4.0.0</modelVersion>
  <parent>
    <groupId>org.example</groupId>
    <artifactId>parent</artifactId>
    <version>1.0</version>
  </parent>
  <artifactId>app</artifactId>
</project>
`,
	})

	result, err := Provider{}.Detect(provider.Context{RepositoryRoot: root, ProjectPath: "app"})
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if hasRuntime(result, "8") {
		t.Fatalf("neighbor POM with a different groupId supplied Java 8: %+v", result.Findings)
	}
}

func TestDetectInheritedSpringBootAndPluginKeepParentEvidence(t *testing.T) {
	t.Parallel()

	root := writeFiles(t, map[string]string{
		"pom.xml": `<project>
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId>
  <artifactId>parent</artifactId>
  <version>1.0</version>
  <packaging>pom</packaging>
  <modules><module>lib</module></modules>
  <dependencies>
    <dependency>
      <groupId>org.springframework.boot</groupId>
      <artifactId>spring-boot-starter-web</artifactId>
    </dependency>
  </dependencies>
  <build>
    <plugins>
      <plugin><artifactId>maven-checkstyle-plugin</artifactId></plugin>
    </plugins>
  </build>
</project>
`,
		"lib/pom.xml": `<project>
  <modelVersion>4.0.0</modelVersion>
  <parent>
    <groupId>com.example</groupId>
    <artifactId>parent</artifactId>
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
	if !hasProperty(result, plan.PropertyFramework, "spring-boot") {
		t.Fatalf("missing inherited Spring Boot in %+v", result.Findings)
	}
	for _, finding := range result.Findings {
		item, ok := finding.(plan.PropertyFinding)
		if !ok || item.Property.Kind != plan.PropertyFramework || item.Property.Name != "spring-boot" {
			continue
		}
		for _, evidence := range item.Property.Evidence {
			if evidence.Source != "pom.xml" {
				t.Fatalf("inherited Spring Boot evidence source = %q, want parent pom.xml", evidence.Source)
			}
		}
	}
	if !slices.Contains(factValues(result, "tool.configured"), "checkstyle") {
		t.Fatalf("missing inherited checkstyle in %+v", result.Findings)
	}
	for _, finding := range result.Findings {
		item, ok := finding.(plan.PropertyFinding)
		if !ok || item.Property.Kind != plan.PropertyFact || item.Property.Name != "tool.configured" || item.Property.Value != "checkstyle" {
			continue
		}
		for _, evidence := range item.Property.Evidence {
			if evidence.Source != "pom.xml" {
				t.Fatalf("inherited checkstyle evidence source = %q, want parent pom.xml", evidence.Source)
			}
		}
	}
}

func TestDetectCompetingManagersOnlyAmbiguatesSupportedServer(t *testing.T) {
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
  <artifactId>demo</artifactId>
  <build>
    <plugins>
      <plugin>
        <groupId>org.springframework.boot</groupId>
        <artifactId>spring-boot-maven-plugin</artifactId>
      </plugin>
    </plugins>
  </build>
</project>
`,
		"build.gradle": `plugins { id 'java' }`,
		"mvnw":         "#!/bin/sh\n",
		"gradlew":      "#!/bin/sh\n",
		"src/main/java/com/example/DemoApplication.java": "public class DemoApplication { public static void main(String[] args) {} }\n",
	})

	if slices.Contains(ambiguitySubjects(result), "application.run") {
		t.Fatalf("application.run ambiguity cited an unsupported gradle bootRun: %+v", result.Ambiguities)
	}
	assertCommand(t, commandsByName(result)["server"], "./mvnw spring-boot:run", plan.CapabilityApplicationRun)
}

func TestDetectWrapperEvidenceOnInferredCommands(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pom.xml":                    `<project><modelVersion>4.0.0</modelVersion><artifactId>lib</artifactId></project>`,
		"mvnw":                       "#!/bin/sh\n",
		"src/test/java/LibTest.java": "class LibTest {}\n",
	})

	command := commandsByName(result)["test"]
	var sources []string
	for _, evidence := range command.Evidence {
		sources = append(sources, evidence.Source)
	}
	if !slices.Contains(sources, "mvnw") {
		t.Fatalf("test evidence = %v, want wrapper file mvnw", sources)
	}
}

func TestDetectMavenSpringBootWithoutPluginDoesNotInferServer(t *testing.T) {
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
  <artifactId>demo</artifactId>
  <dependencies>
    <dependency>
      <groupId>org.springframework.boot</groupId>
      <artifactId>spring-boot-starter-web</artifactId>
    </dependency>
  </dependencies>
</project>
`,
		"src/main/java/com/example/DemoApplication.java": "public class DemoApplication { public static void main(String[] args) {} }\n",
	})

	if !hasProperty(result, plan.PropertyFramework, "spring-boot") {
		t.Fatalf("missing Spring Boot framework in %+v", result.Findings)
	}
	if _, ok := commandsByName(result)["server"]; ok {
		t.Fatal("inferred spring-boot:run without spring-boot-maven-plugin")
	}
}

func TestDetectGradleStarterWithoutPluginDoesNotInferBootRun(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"build.gradle": `plugins { id 'java' }
dependencies { implementation 'org.springframework.boot:spring-boot-starter-web' }
`,
		"src/main/java/com/example/DemoApplication.java": "public class DemoApplication { public static void main(String[] args) {} }\n",
	})

	if !hasProperty(result, plan.PropertyFramework, "spring-boot") {
		t.Fatalf("missing Spring Boot framework in %+v", result.Findings)
	}
	if _, ok := commandsByName(result)["server"]; ok {
		t.Fatal("inferred bootRun without the Spring Boot Gradle plugin")
	}
}

func TestDetectGradleApplicationWithoutMainClassDoesNotInferRun(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"build.gradle":                       `plugins { id 'application' }`,
		"src/main/java/com/example/App.java": "public class App { public static void main(String[] args) {} }\n",
	})

	if _, ok := commandsByName(result)["server"]; ok {
		t.Fatal("inferred gradle run without application.mainClass")
	}
}

func TestDetectGradleApplicationWithMainClassInfersRun(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"build.gradle.kts": `plugins { id("application") }
application { mainClass.set("com.example.App") }
`,
		"src/main/java/com/example/App.java": "public class App { public static void main(String[] args) {} }\n",
	})

	command := commandsByName(result)["server"]
	assertCommand(t, command, "gradle run", plan.CapabilityApplicationRun)
	var pointers []string
	for _, evidence := range command.Evidence {
		if evidence.Pointer != "" {
			pointers = append(pointers, evidence.Pointer)
		}
	}
	if !slices.Contains(pointers, "/application/mainClass") {
		t.Fatalf("run evidence pointers = %v, want /application/mainClass", pointers)
	}
}

func TestDetectMavenSpotlessPlugin(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pom.xml": `<project>
  <modelVersion>4.0.0</modelVersion>
  <artifactId>lib</artifactId>
  <build>
    <plugins>
      <plugin>
        <groupId>com.diffplug.spotless</groupId>
        <artifactId>spotless-maven-plugin</artifactId>
      </plugin>
    </plugins>
  </build>
</project>
`,
	})

	if !slices.Contains(factValues(result, "tool.configured"), "spotless") {
		t.Fatalf("configured tools = %v, want spotless", factValues(result, "tool.configured"))
	}
}

func TestDetectSpringBootLibraryDoesNotInferServer(t *testing.T) {
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
  <artifactId>lib</artifactId>
</project>
`,
		"src/main/java/com/example/Library.java": "package com.example;\npublic class Library {}\n",
	})

	if !hasProperty(result, plan.PropertyFramework, "spring-boot") {
		t.Fatalf("missing Spring Boot framework in %+v", result.Findings)
	}
	if _, ok := commandsByName(result)["server"]; ok {
		t.Fatal("inferred spring-boot:run without an application entry point")
	}
}

func TestDetectSkipsGradleOnIncludedMemberDirectory(t *testing.T) {
	t.Parallel()

	root := writeFiles(t, map[string]string{
		"settings.gradle.kts":  "rootProject.name = \"demo\"\ninclude(\"lib\")\n",
		"build.gradle.kts":     "plugins { id(\"java\") }\n",
		"gradlew":              "#!/bin/sh\n",
		"lib/build.gradle.kts": "plugins { id(\"java\") }\n",
		"lib/package.json":     `{"name":"lib"}`,
	})

	result, err := Provider{}.Detect(provider.Context{RepositoryRoot: root, ProjectPath: "lib"})
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if hasPackageManager(result, "gradle", "") {
		t.Fatalf("included Gradle member was treated as a standalone Gradle project: %+v", result.Findings)
	}
	if _, ok := commandsByName(result)["build"]; ok {
		t.Fatal("included member emitted a Gradle build command")
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

func TestDetectMavenLegacyJava18NormalizesTo8(t *testing.T) {
	t.Parallel()

	result := detectFiles(t, map[string]string{
		"pom.xml": `<project>
  <modelVersion>4.0.0</modelVersion>
  <properties><maven.compiler.source>1.8</maven.compiler.source></properties>
</project>
`,
		".java-version": "8\n",
	})

	if hasRuntime(result, "1.8") {
		t.Fatalf("legacy Maven Java 1.8 was not normalized: %+v", result.Findings)
	}
	if !hasRuntime(result, "8") {
		t.Fatalf("missing normalized Java 8 in %+v", result.Findings)
	}
	if len(result.Conflicts) != 0 {
		t.Fatalf("conflicts = %+v, want none between Maven 1.8 and Java 8", result.Conflicts)
	}
	for _, finding := range result.Findings {
		item, ok := finding.(plan.RequirementFinding)
		if !ok || item.Requirement.Name != "java" || item.Requirement.Version != "8" {
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
	t.Fatal("missing merged Java 8 requirement")
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

func evidenceSources(evidence []plan.Evidence) []string {
	out := make([]string, 0, len(evidence))
	for _, item := range evidence {
		out = append(out, item.Source)
	}
	return out
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
