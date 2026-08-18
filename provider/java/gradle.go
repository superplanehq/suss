package java

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

type gradleProject struct {
	Source            string
	SettingsFile      string
	MultiProject      bool
	JavaVersion       string
	JavaVersionPtr    string
	SpringBoot        bool
	SpringPointer     string
	ApplicationPlugin bool
	Plugins           map[string]struct{}
	Wrapper           string
	WrapperVersion    string
	WrapperProperties string
}

func readGradle(ctx provider.Context) (*gradleProject, error) {
	source, contents, ok, err := readFirstFile(ctx.ProjectDir(), []string{"build.gradle.kts", "build.gradle", "settings.gradle.kts", "settings.gradle"})
	if err != nil || !ok {
		return nil, err
	}
	settings, settingsContents, hasSettings, err := readFirstFile(ctx.ProjectDir(), []string{"settings.gradle.kts", "settings.gradle"})
	if err != nil {
		return nil, err
	}

	buildContents := contents
	if source == "settings.gradle" || source == "settings.gradle.kts" {
		if buildSource, build, found, buildErr := readFirstFile(ctx.ProjectDir(), []string{"build.gradle.kts", "build.gradle"}); buildErr != nil {
			return nil, buildErr
		} else if found {
			source = buildSource
			buildContents = build
		} else {
			buildContents = ""
		}
	}

	stripped := stripGradleComments(buildContents + "\n" + settingsContents)
	project := &gradleProject{
		Source:       source,
		SettingsFile: settings,
		MultiProject: hasSettings && hasGradleIncludes(settingsContents+"\n"+buildContents),
		Plugins:      gradlePlugins(stripped),
	}
	project.JavaVersion, project.JavaVersionPtr = gradleJavaVersion(stripped)
	project.SpringBoot, project.SpringPointer = gradleSpringBoot(stripped, project.Plugins)
	_, project.ApplicationPlugin = project.Plugins["application"]
	project.Wrapper, project.WrapperVersion, project.WrapperProperties = gradleWrapper(ctx.ProjectDir())
	return project, nil
}

func readFirstFile(dir string, names []string) (string, string, bool, error) {
	for _, name := range names {
		contents, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", "", false, err
		}
		return name, string(contents), true, nil
	}
	return "", "", false, nil
}

func hasGradleIncludes(contents string) bool {
	if strings.Contains(contents, "include(") {
		return true
	}
	return regexp.MustCompile(`(?m)^\s*include\s+['"]`).MatchString(contents)
}

func gradlePlugins(contents string) map[string]struct{} {
	out := make(map[string]struct{})
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?m)\bid\s*\(\s*["']([^"']+)["']`),
		regexp.MustCompile(`(?m)\bid\s+["']([^"']+)["']`),
		regexp.MustCompile(`(?m)apply\s+plugin:\s*["']([^"']+)["']`),
	}
	for _, pattern := range patterns {
		for _, match := range pattern.FindAllStringSubmatch(contents, -1) {
			if len(match) == 2 {
				out[match[1]] = struct{}{}
			}
		}
	}
	return out
}

func gradleJavaVersion(contents string) (string, string) {
	patterns := []struct {
		pattern *regexp.Regexp
		pointer string
	}{
		{regexp.MustCompile(`JavaLanguageVersion\.of\(\s*["']?(\d+)["']?\s*\)`), "/java/toolchain"},
		{regexp.MustCompile(`JavaVersion\.VERSION_(\d+)`), "/sourceCompatibility"},
		{regexp.MustCompile(`(?m)sourceCompatibility\s*=\s*["']?(\d+)["']?`), "/sourceCompatibility"},
		{regexp.MustCompile(`(?m)targetCompatibility\s*=\s*["']?(\d+)["']?`), "/targetCompatibility"},
		{regexp.MustCompile(`(?m)jvmTarget\s*=\s*["']?(\d+)["']?`), "/jvmTarget"},
	}
	for _, candidate := range patterns {
		if match := candidate.pattern.FindStringSubmatch(contents); len(match) == 2 {
			return match[1], candidate.pointer
		}
	}
	return "", ""
}

func gradleSpringBoot(contents string, plugins map[string]struct{}) (bool, string) {
	if _, ok := plugins["org.springframework.boot"]; ok {
		return true, "/plugins/org.springframework.boot"
	}
	if strings.Contains(contents, "org.springframework.boot:spring-boot-starter") || strings.Contains(contents, `"org.springframework.boot:spring-boot-starter`) {
		return true, "/dependencies/spring-boot-starter"
	}
	return false, ""
}

func (g *gradleProject) springBootEvidence(ctx provider.Context) []plan.Evidence {
	if g == nil || !g.SpringBoot {
		return nil
	}
	return []plan.Evidence{{
		Kind:        plan.EvidenceDeclaration,
		Source:      ctx.SourcePath(g.Source),
		Pointer:     g.SpringPointer,
		Description: "The Gradle build declares Spring Boot.",
	}}
}

func (g *gradleProject) hasPlugin(id string) bool {
	if g == nil {
		return false
	}
	_, ok := g.Plugins[id]
	return ok
}

func gradleWrapper(dir string) (script, version, properties string) {
	if fileExists(dir, "gradlew") {
		script = "./gradlew"
	} else if fileExists(dir, "gradlew.bat") {
		script = "gradlew.bat"
	}
	name := "gradle/wrapper/gradle-wrapper.properties"
	if fileExists(dir, name) {
		properties = name
		version = wrapperVersionFromURL(readFileIfPresent(dir, name), "gradle-")
	}
	return script, version, properties
}

func stripGradleComments(contents string) string {
	var out strings.Builder
	inSingle, inDouble, inBlock := false, false, false
	for i := 0; i < len(contents); i++ {
		if inBlock {
			if contents[i] == '*' && i+1 < len(contents) && contents[i+1] == '/' {
				inBlock = false
				i++
			}
			continue
		}
		if inSingle || inDouble {
			out.WriteByte(contents[i])
			if inSingle && contents[i] == '\'' && !escaped(contents, i) {
				inSingle = false
			}
			if inDouble && contents[i] == '"' && !escaped(contents, i) {
				inDouble = false
			}
			continue
		}
		if contents[i] == '/' && i+1 < len(contents) && contents[i+1] == '/' {
			for i < len(contents) && contents[i] != '\n' {
				i++
			}
			if i < len(contents) {
				out.WriteByte('\n')
			}
			continue
		}
		if contents[i] == '/' && i+1 < len(contents) && contents[i+1] == '*' {
			inBlock = true
			i++
			continue
		}
		switch contents[i] {
		case '\'':
			inSingle = true
		case '"':
			inDouble = true
		}
		out.WriteByte(contents[i])
	}
	return out.String()
}

func escaped(contents string, i int) bool {
	slashes := 0
	for i > 0 && contents[i-1] == '\\' {
		slashes++
		i--
	}
	return slashes%2 == 1
}

func readFileIfPresent(dir, name string) string {
	contents, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		return ""
	}
	return string(contents)
}

var (
	gradleDistPattern = regexp.MustCompile(`gradle-([0-9][^/]*?)(?:-bin|-all|-src)?\.zip`)
	mavenDistPattern  = regexp.MustCompile(`apache-maven-([0-9][^/]*?)-bin\.zip`)
)

func wrapperVersionFromURL(contents, prefix string) string {
	var pattern *regexp.Regexp
	switch prefix {
	case "gradle-":
		pattern = gradleDistPattern
	default:
		pattern = mavenDistPattern
	}
	if match := pattern.FindStringSubmatch(contents); len(match) == 2 {
		return match[1]
	}
	return ""
}
