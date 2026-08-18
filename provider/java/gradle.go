package java

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

type gradleProject struct {
	Source            string
	SettingsFile      string
	Members           []string
	MultiProject      bool
	JavaVersion       string
	JavaVersionPtr    string
	SpringBoot        bool
	SpringPointer     string
	ApplicationPlugin bool
	Plugins           map[string]struct{}
	Wrapper           string
	WrapperSource     string
	WrapperVersion    string
	WrapperProperties string
}

func readGradle(ctx provider.Context) (*gradleProject, error) {
	settings, settingsContents, hasSettings, err := readFirstFile(ctx.ProjectDir(), []string{"settings.gradle.kts", "settings.gradle"})
	if err != nil {
		return nil, err
	}
	if !hasSettings {
		included, includedErr := includedByAncestorSettings(ctx)
		if includedErr != nil || included {
			return nil, includedErr
		}
	}

	source, contents, ok, err := readFirstFile(ctx.ProjectDir(), []string{"build.gradle.kts", "build.gradle", "settings.gradle.kts", "settings.gradle"})
	if err != nil || !ok {
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

	members := SettingsIncludes(settingsContents)
	stripped := stripGradleComments(buildContents)
	project := &gradleProject{
		Source:       source,
		SettingsFile: settings,
		Members:      members,
		MultiProject: hasSettings && len(members) > 0,
		Plugins:      gradlePlugins(stripped),
	}
	project.JavaVersion, project.JavaVersionPtr = gradleJavaVersion(stripped)
	project.SpringBoot, project.SpringPointer = gradleSpringBoot(stripped, project.Plugins)
	_, project.ApplicationPlugin = project.Plugins["application"]
	project.Wrapper, project.WrapperSource, project.WrapperVersion, project.WrapperProperties = gradleWrapper(ctx)
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

func includedByAncestorSettings(ctx provider.Context) (bool, error) {
	if ctx.ProjectPath == "." || ctx.ProjectPath == "" {
		return false, nil
	}
	projectDir := ctx.ProjectDir()
	dir := filepath.Dir(projectDir)
	for {
		var contents strings.Builder
		for _, name := range []string{"settings.gradle.kts", "settings.gradle"} {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err == nil {
				contents.Write(data)
				contents.WriteByte('\n')
				continue
			}
			if !os.IsNotExist(err) {
				return false, err
			}
		}
		if contents.Len() > 0 {
			rel, err := filepath.Rel(dir, projectDir)
			if err != nil {
				return false, err
			}
			if settingsIncludeContains(SettingsIncludes(contents.String()), filepath.ToSlash(rel)) {
				return true, nil
			}
		}
		if dir == ctx.RepositoryRoot {
			return false, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir || !insideRepository(ctx.RepositoryRoot, parent) {
			return false, nil
		}
		dir = parent
	}
}

func settingsIncludeContains(listed []string, relative string) bool {
	return relative != "" && slices.Contains(listed, relative)
}

var (
	gradleIncludeCall = regexp.MustCompile(`(?m)(?:^|[^\w])include\s*\(([^)]*)\)`)
	gradleIncludeBare = regexp.MustCompile(`(?m)(?:^|[^\w])include\s+([^;\n]+)`)
	gradleQuoted      = regexp.MustCompile(`["']([^"']+)["']`)
)

// SettingsIncludes returns member paths declared by include(...) in a Gradle
// settings file. Line and block comments are ignored.
func SettingsIncludes(contents string) []string {
	contents = stripGradleComments(contents)
	var paths []string
	seen := make(map[string]struct{})
	add := func(raw string) {
		raw = strings.TrimSpace(raw)
		raw = strings.Trim(raw, ":")
		raw = strings.ReplaceAll(raw, ":", "/")
		if raw == "" {
			return
		}
		if _, ok := seen[raw]; ok {
			return
		}
		seen[raw] = struct{}{}
		paths = append(paths, raw)
	}
	for _, match := range gradleIncludeCall.FindAllStringSubmatch(contents, -1) {
		for _, quoted := range gradleQuoted.FindAllStringSubmatch(match[1], -1) {
			add(quoted[1])
		}
	}
	for _, match := range gradleIncludeBare.FindAllStringSubmatch(contents, -1) {
		for _, quoted := range gradleQuoted.FindAllStringSubmatch(match[1], -1) {
			add(quoted[1])
		}
	}
	return paths
}

func gradlePlugins(contents string) map[string]struct{} {
	out := make(map[string]struct{})
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?m)\bid\s*\(\s*["']([^"']+)["']\s*\)([^\n]*)`),
		regexp.MustCompile(`(?m)\bid\s+["']([^"']+)["']([^\n]*)`),
	}
	for _, pattern := range patterns {
		for _, match := range pattern.FindAllStringSubmatch(contents, -1) {
			if len(match) < 2 || gradleApplyFalse.MatchString(match[2]) {
				continue
			}
			out[match[1]] = struct{}{}
		}
	}
	for _, match := range gradleApplyPlugin.FindAllStringSubmatch(contents, -1) {
		if len(match) == 2 {
			out[match[1]] = struct{}{}
		}
	}
	return out
}

var (
	gradleApplyFalse  = regexp.MustCompile(`(?i)\bapply\s*(?:false|\(\s*false\s*\))`)
	gradleApplyPlugin = regexp.MustCompile(`(?m)apply\s+plugin:\s*["']([^"']+)["']`)
)

func gradleJavaVersion(contents string) (string, string) {
	patterns := []struct {
		pattern *regexp.Regexp
		pointer string
	}{
		{regexp.MustCompile(`JavaLanguageVersion\.of\(\s*["']?(\d+)["']?\s*\)`), "/java/toolchain"},
		{regexp.MustCompile(`JavaVersion\.VERSION_(\d+(?:_\d+)?)`), "/sourceCompatibility"},
		{regexp.MustCompile(`(?m)sourceCompatibility\s*=\s*["']?(\d+(?:\.\d+)?)["']?`), "/sourceCompatibility"},
		{regexp.MustCompile(`(?m)targetCompatibility\s*=\s*["']?(\d+(?:\.\d+)?)["']?`), "/targetCompatibility"},
		{regexp.MustCompile(`(?m)jvmTarget\s*=\s*["']?(\d+(?:\.\d+)?)["']?`), "/jvmTarget"},
	}
	for _, candidate := range patterns {
		if match := candidate.pattern.FindStringSubmatch(contents); len(match) == 2 {
			return normalizeJavaVersion(match[1]), candidate.pointer
		}
	}
	return "", ""
}

func normalizeJavaVersion(raw string) string {
	raw = strings.ReplaceAll(strings.TrimSpace(raw), "_", ".")
	if strings.HasPrefix(raw, "1.") {
		rest := strings.TrimPrefix(raw, "1.")
		if rest != "" && !strings.Contains(rest, ".") {
			return rest
		}
	}
	return raw
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

func gradleWrapper(ctx provider.Context) (script, source, version, properties string) {
	dir := ctx.ProjectDir()
	name := ""
	switch {
	case fileExists(dir, "gradlew"):
		name = "gradlew"
		script = "./gradlew"
	case fileExists(dir, "gradlew.bat"):
		name = "gradlew.bat"
		script = "gradlew.bat"
	}
	if name != "" {
		if rel, err := filepath.Rel(ctx.RepositoryRoot, filepath.Join(dir, name)); err == nil {
			source = filepath.ToSlash(rel)
		}
	}
	propName := "gradle/wrapper/gradle-wrapper.properties"
	if fileExists(dir, propName) {
		properties = propName
		version = wrapperVersionFromURL(readFileIfPresent(dir, propName), "gradle-")
	}
	return script, source, version, properties
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
