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

type gradleJavaPin struct {
	Version string
	Pointer string
	Source  string
}

type gradleProject struct {
	Source            string
	SettingsFile      string
	Members           []string
	MultiProject      bool
	JavaVersion       string
	JavaVersionPtr    string
	JavaVersionSource string
	JavaPins          []gradleJavaPin
	SpringBoot        bool
	SpringPointer     string
	SpringSource      string
	Plugins           map[string]string
	Wrapper           string
	WrapperSource     string
	WrapperVersion    string
	WrapperProperties string
	Builds            []gradleBuild
}

type gradleBuild struct {
	Member            string
	Source            string
	Plugins           map[string]struct{}
	ApplicationPlugin bool
	MainClass         string
	MainClassPointer  string
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

	members := repoMembers(ctx, SettingsIncludes(settingsContents))
	project := &gradleProject{
		Source:       source,
		SettingsFile: settings,
		Members:      members,
		MultiProject: hasSettings && len(members) > 0,
		Plugins:      map[string]string{},
	}
	absorbGradleBuild(project, "", ctx.SourcePath(source), stripGradleComments(buildContents))
	if err := absorbMemberBuilds(ctx, project); err != nil {
		return nil, err
	}
	project.Wrapper, project.WrapperSource, project.WrapperVersion, project.WrapperProperties = gradleWrapper(ctx)
	if !project.hasSupportedJavaPlugin() {
		return nil, nil
	}
	return project, nil
}

func absorbMemberBuilds(ctx provider.Context, project *gradleProject) error {
	for _, member := range project.Members {
		dir, ok := repoMemberDir(ctx, member)
		if !ok {
			continue
		}
		name, contents, found, err := readFirstFile(dir, []string{"build.gradle.kts", "build.gradle"})
		if err != nil {
			return err
		}
		if !found {
			continue
		}
		absorbGradleBuild(project, member, ctx.SourcePath(filepath.ToSlash(filepath.Join(member, name))), stripGradleComments(contents))
	}
	return nil
}

func repoMembers(ctx provider.Context, members []string) []string {
	kept := make([]string, 0, len(members))
	for _, member := range members {
		if _, ok := repoMemberDir(ctx, member); ok {
			kept = append(kept, member)
		}
	}
	return kept
}

func repoMemberDir(ctx provider.Context, member string) (string, bool) {
	member = strings.TrimSpace(member)
	if member == "" || filepath.IsAbs(filepath.FromSlash(member)) {
		return "", false
	}
	cleaned := filepath.Clean(filepath.FromSlash(member))
	if cleaned == "." || cleaned == string(filepath.Separator) {
		return "", false
	}
	memberDir := filepath.Join(ctx.ProjectDir(), cleaned)
	if !insideRepository(ctx.RepositoryRoot, memberDir) {
		return "", false
	}
	return memberDir, true
}

func absorbGradleBuild(project *gradleProject, member, source, contents string) {
	plugins := gradlePlugins(contents)
	build := gradleBuild{Member: member, Source: source, Plugins: plugins}
	for id := range plugins {
		if project.Plugins[id] == "" {
			project.Plugins[id] = source
		}
	}
	if version, pointer := gradleToolchainVersion(contents); version != "" {
		project.JavaPins = append(project.JavaPins, gradleJavaPin{Version: version, Pointer: pointer, Source: source})
		if project.JavaVersion == "" {
			project.JavaVersion = version
			project.JavaVersionPtr = pointer
			project.JavaVersionSource = source
		}
	}
	if ok, pointer := gradleSpringBoot(contents, plugins); ok {
		if !project.SpringBoot || (pointer == "/plugins/org.springframework.boot" && project.SpringPointer != pointer) {
			project.SpringBoot = true
			project.SpringPointer = pointer
			project.SpringSource = source
		}
	}
	if _, ok := plugins["application"]; ok {
		build.ApplicationPlugin = true
	}
	if class, pointer := gradleMainClass(contents); class != "" {
		build.MainClass = class
		build.MainClassPointer = pointer
	}
	project.Builds = append(project.Builds, build)
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
	for _, block := range gradlePluginsBlocks(contents) {
		addGradlePluginAccessors(out, block)
	}
	return out
}

var supportedJavaPlugins = []string{
	"java",
	"java-library",
	"java-gradle-plugin",
	"application",
	"org.springframework.boot",
}

var gradleCorePlugins = map[string]struct{}{
	"application":        {},
	"checkstyle":         {},
	"groovy":             {},
	"java":               {},
	"java-gradle-plugin": {},
	"java-library":       {},
	"jacoco":             {},
	"pmd":                {},
	"scala":              {},
	"war":                {},
}

var gradlePluginAccessorKeywords = map[string]struct{}{
	"alias":   {},
	"apply":   {},
	"false":   {},
	"id":      {},
	"kotlin":  {},
	"true":    {},
	"version": {},
}

var (
	gradleApplyFalse       = regexp.MustCompile(`(?i)\bapply\s*(?:false|\(\s*false\s*\))`)
	gradleApplyPlugin      = regexp.MustCompile(`(?m)apply\s+plugin:\s*["']([^"']+)["']`)
	gradleBacktickPlugin   = regexp.MustCompile("`([a-z][a-z0-9-]*)`")
	gradleBarePlugin       = regexp.MustCompile(`(?m)(?:^|[{\s,])([a-z][a-z0-9-]*)\b`)
	gradleToolchainPattern = regexp.MustCompile(`JavaLanguageVersion\.of\(\s*["']?(\d+)["']?\s*\)`)
)

func gradlePluginsBlocks(contents string) []string {
	var blocks []string
	for i := 0; i < len(contents); {
		idx := strings.Index(contents[i:], "plugins")
		if idx < 0 {
			break
		}
		idx += i
		if idx > 0 && isGradleIdentChar(contents[idx-1]) {
			i = idx + len("plugins")
			continue
		}
		after := idx + len("plugins")
		if after < len(contents) && isGradleIdentChar(contents[after]) {
			i = after
			continue
		}
		open := skipGradleSpace(contents, after)
		if open >= len(contents) || contents[open] != '{' {
			i = after
			continue
		}
		block, end, ok := readGradleBraceBlock(contents, open)
		if !ok {
			break
		}
		blocks = append(blocks, block)
		i = end
	}
	return blocks
}

func addGradlePluginAccessors(out map[string]struct{}, block string) {
	add := func(name, line string) {
		if _, known := gradleCorePlugins[name]; !known {
			return
		}
		if gradleApplyFalse.MatchString(line) {
			return
		}
		out[name] = struct{}{}
	}
	for _, match := range gradleBacktickPlugin.FindAllStringSubmatchIndex(block, -1) {
		add(block[match[2]:match[3]], gradleLineAt(block, match[0]))
	}
	for _, match := range gradleBarePlugin.FindAllStringSubmatchIndex(block, -1) {
		name := block[match[2]:match[3]]
		if _, skip := gradlePluginAccessorKeywords[name]; skip {
			continue
		}
		rest := strings.TrimSpace(block[match[3]:])
		if strings.HasPrefix(rest, "(") {
			continue
		}
		add(name, gradleLineAt(block, match[0]))
	}
}

func gradleLineAt(contents string, index int) string {
	start, end := index, index
	for start > 0 && contents[start-1] != '\n' {
		start--
	}
	for end < len(contents) && contents[end] != '\n' {
		end++
	}
	return contents[start:end]
}

func isGradleIdentChar(b byte) bool {
	return b == '_' || b == '-' || (b >= '0' && b <= '9') || (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

func skipGradleSpace(contents string, i int) int {
	for i < len(contents) && (contents[i] == ' ' || contents[i] == '\t' || contents[i] == '\n' || contents[i] == '\r') {
		i++
	}
	return i
}

func readGradleBraceBlock(contents string, open int) (string, int, bool) {
	depth := 0
	for i := open; i < len(contents); i++ {
		switch contents[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return contents[open+1 : i], i + 1, true
			}
		}
	}
	return "", open, false
}

func gradleToolchainVersion(contents string) (string, string) {
	if match := gradleToolchainPattern.FindStringSubmatch(contents); len(match) == 2 {
		return normalizeJavaVersion(match[1]), "/java/toolchain"
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
	source := g.SpringSource
	if source == "" {
		source = ctx.SourcePath(g.Source)
	}
	return []plan.Evidence{{
		Kind:        plan.EvidenceDeclaration,
		Source:      source,
		Pointer:     g.SpringPointer,
		Description: "The Gradle build declares Spring Boot.",
	}}
}

func (g *gradleProject) hasPlugin(id string) bool {
	return g.pluginSource(id) != ""
}

func (g *gradleProject) pluginSource(id string) string {
	if g == nil {
		return ""
	}
	return g.Plugins[id]
}

func (g *gradleProject) hasSupportedJavaPlugin() bool {
	if g == nil {
		return false
	}
	for _, id := range supportedJavaPlugins {
		if g.hasPlugin(id) {
			return true
		}
	}
	return false
}

func (g *gradleProject) javaPluginEvidence() []plan.Evidence {
	if g == nil {
		return nil
	}
	for _, id := range supportedJavaPlugins {
		if source := g.pluginSource(id); source != "" {
			return []plan.Evidence{{
				Kind:    plan.EvidenceDeclaration,
				Source:  source,
				Pointer: "/plugins/" + pointerToken(id),
			}}
		}
	}
	return nil
}

func (b gradleBuild) hasSpringBootPlugin() bool {
	_, ok := b.Plugins["org.springframework.boot"]
	return ok
}

func (b gradleBuild) canRunApplication() bool {
	return b.ApplicationPlugin && b.MainClass != ""
}

func gradleMainClass(contents string) (string, string) {
	patterns := []struct {
		pattern *regexp.Regexp
		pointer string
	}{
		{regexp.MustCompile(`(?m)\bmainClass(?:Name)?\s*\.\s*set\s*\(\s*["']([^"']+)["']\s*\)`), "/application/mainClass"},
		{regexp.MustCompile(`(?m)\bmainClassName\s*=\s*["']([^"']+)["']`), "/mainClassName"},
		{regexp.MustCompile(`(?m)\bmainClass\s*=\s*["']([^"']+)["']`), "/application/mainClass"},
	}
	for _, candidate := range patterns {
		if match := candidate.pattern.FindStringSubmatch(contents); len(match) == 2 && strings.TrimSpace(match[1]) != "" {
			return match[1], candidate.pointer
		}
	}
	return "", ""
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
