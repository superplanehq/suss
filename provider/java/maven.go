package java

import (
	"encoding/xml"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

type mavenProject struct {
	Source            string
	JavaVersion       string
	JavaVersionPtr    string
	JavaVersionSource string
	Aggregator        bool
	Modules           []string
	SpringBoot        bool
	SpringPointer     string
	SpringSource      string
	Plugins           map[string]string
	Wrapper           string
	WrapperSource     string
	WrapperVersion    string
	WrapperProperties string
}

type pomDocument struct {
	XMLName          xml.Name      `xml:"project"`
	Source           string        `xml:"-"`
	GroupID          string        `xml:"groupId"`
	ArtifactID       string        `xml:"artifactId"`
	Version          string        `xml:"version"`
	Packaging        string        `xml:"packaging"`
	Parent           pomParent     `xml:"parent"`
	Properties       pomProperties `xml:"properties"`
	Modules          []string      `xml:"modules>module"`
	Dependencies     []pomArtifact `xml:"dependencies>dependency"`
	Plugins          []pomPlugin   `xml:"build>plugins>plugin"`
	PluginManagement []pomPlugin   `xml:"build>pluginManagement>plugins>plugin"`
}

type pomParent struct {
	GroupID      string  `xml:"groupId"`
	ArtifactID   string  `xml:"artifactId"`
	Version      string  `xml:"version"`
	RelativePath *string `xml:"relativePath"`
}

type pomArtifact struct {
	GroupID    string `xml:"groupId"`
	ArtifactID string `xml:"artifactId"`
	Source     string `xml:"-"`
}

type pomPlugin struct {
	GroupID       string `xml:"groupId"`
	ArtifactID    string `xml:"artifactId"`
	Inherited     string `xml:"inherited"`
	Source        string `xml:"-"`
	Configuration struct {
		Release string `xml:"release"`
		Source  string `xml:"source"`
		Target  string `xml:"target"`
	} `xml:"configuration"`
	ReleaseSource string `xml:"-"`
	SourceSource  string `xml:"-"`
	TargetSource  string `xml:"-"`
}

type pomProperties struct {
	Entries []pomProperty `xml:",any"`
}

type pomProperty struct {
	XMLName xml.Name
	Value   string `xml:",chardata"`
	Source  string `xml:"-"`
}

var xmlnsAttrPattern = regexp.MustCompile(`\sxmlns(?::[A-Za-z_][\w.-]*)?="[^"]*"`)

func readMaven(ctx provider.Context) (*mavenProject, error) {
	parsed, ok, err := loadPOMFile(filepath.Join(ctx.ProjectDir(), "pom.xml"), ctx.RepositoryRoot, map[string]struct{}{})
	if err != nil || !ok {
		return nil, err
	}
	project := &mavenProject{
		Source:     "pom.xml",
		Aggregator: strings.TrimSpace(parsed.Packaging) == "pom" && len(parsed.Modules) > 0,
		Modules:    normalizeMemberPaths(parsed.Modules),
		Plugins:    pluginSet(parsed),
	}
	project.JavaVersion, project.JavaVersionPtr, project.JavaVersionSource = pomJavaVersion(parsed)
	project.SpringBoot, project.SpringPointer, project.SpringSource = pomSpringBoot(parsed)
	project.Wrapper, project.WrapperSource, project.WrapperVersion, project.WrapperProperties = mavenWrapper(ctx)
	return project, nil
}

func loadPOMFile(path, repoRoot string, seen map[string]struct{}) (pomDocument, bool, error) {
	canonical, err := filepath.Abs(path)
	if err != nil {
		return pomDocument{}, false, fmt.Errorf("resolve pom: %w", err)
	}
	if _, dup := seen[canonical]; dup {
		return pomDocument{}, false, nil
	}
	if !insideRepository(repoRoot, canonical) {
		return pomDocument{}, false, nil
	}

	contents, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return pomDocument{}, false, nil
	}
	if err != nil {
		return pomDocument{}, false, fmt.Errorf("read pom.xml: %w", err)
	}

	parsed, err := parsePOM(string(contents))
	if err != nil {
		return pomDocument{}, false, fmt.Errorf("parse pom.xml: %w", err)
	}
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return pomDocument{}, false, fmt.Errorf("resolve repository root: %w", err)
	}
	relative, err := filepath.Rel(root, canonical)
	if err != nil {
		return pomDocument{}, false, fmt.Errorf("resolve pom source: %w", err)
	}
	stampPOM(&parsed, filepath.ToSlash(relative))
	seen[canonical] = struct{}{}

	parent, ok, err := loadParentPOM(filepath.Dir(path), repoRoot, parsed.Parent, seen)
	if err != nil {
		return pomDocument{}, false, err
	}
	if ok {
		parsed = mergePOM(parent, parsed)
	}
	return parsed, true, nil
}

func loadParentPOM(dir, repoRoot string, parent pomParent, seen map[string]struct{}) (pomDocument, bool, error) {
	if strings.TrimSpace(parent.ArtifactID) == "" {
		return pomDocument{}, false, nil
	}
	relative := "../pom.xml"
	if parent.RelativePath != nil {
		if strings.TrimSpace(*parent.RelativePath) == "" {
			return pomDocument{}, false, nil
		}
		relative = strings.TrimSpace(*parent.RelativePath)
	}
	abs := filepath.Join(dir, filepath.FromSlash(relative))
	info, err := os.Stat(abs)
	if err != nil {
		return pomDocument{}, false, nil
	}
	if info.IsDir() {
		abs = filepath.Join(abs, "pom.xml")
	}
	candidate, ok, err := loadPOMFile(abs, repoRoot, seen)
	if err != nil || !ok {
		return pomDocument{}, false, err
	}
	if !parentCoordinatesMatch(parent, candidate) {
		return pomDocument{}, false, nil
	}
	return candidate, true, nil
}

func parentCoordinatesMatch(want pomParent, have pomDocument) bool {
	if strings.TrimSpace(want.ArtifactID) != strings.TrimSpace(have.ArtifactID) {
		return false
	}
	if want.GroupID != "" && strings.TrimSpace(want.GroupID) != strings.TrimSpace(have.GroupID) {
		return false
	}
	if want.Version != "" && strings.TrimSpace(want.Version) != strings.TrimSpace(have.Version) {
		return false
	}
	return true
}

func parsePOM(contents string) (pomDocument, error) {
	stripped := xmlnsAttrPattern.ReplaceAllString(contents, "")
	var parsed pomDocument
	if err := xml.Unmarshal([]byte(stripped), &parsed); err != nil {
		return pomDocument{}, err
	}
	parsed.Properties.resolve()
	return parsed, nil
}

func (p pomProperties) lookup(name string) string {
	for _, entry := range p.Entries {
		if entry.XMLName.Local == name {
			return strings.TrimSpace(entry.Value)
		}
	}
	return ""
}

func (p *pomProperties) resolve() {
	values := propertyMap(*p)
	for i, entry := range p.Entries {
		p.Entries[i].Value = interpolate(entry.Value, values, 0)
	}
}

var propertyPattern = regexp.MustCompile(`\$\{([^{}]+)\}`)

func interpolate(value string, properties map[string]string, depth int) string {
	if depth > 5 || !strings.Contains(value, "${") {
		return strings.TrimSpace(value)
	}
	replaced := propertyPattern.ReplaceAllStringFunc(value, func(match string) string {
		name := strings.TrimSuffix(strings.TrimPrefix(match, "${"), "}")
		if next, ok := properties[name]; ok && next != match {
			return next
		}
		return match
	})
	if replaced == value {
		return strings.TrimSpace(value)
	}
	return interpolate(replaced, properties, depth+1)
}

func stampPOM(parsed *pomDocument, source string) {
	parsed.Source = source
	for i := range parsed.Properties.Entries {
		parsed.Properties.Entries[i].Source = source
	}
	for i := range parsed.Plugins {
		stampPlugin(&parsed.Plugins[i], source)
	}
	for i := range parsed.PluginManagement {
		stampPlugin(&parsed.PluginManagement[i], source)
	}
	for i := range parsed.Dependencies {
		parsed.Dependencies[i].Source = source
	}
}

func mergePOM(parent, child pomDocument) pomDocument {
	merged := child
	if merged.GroupID == "" {
		merged.GroupID = parent.GroupID
	}
	if merged.Version == "" {
		merged.Version = parent.Version
	}
	if merged.Packaging == "" {
		merged.Packaging = parent.Packaging
	}
	seenProps := make(map[string]struct{}, len(child.Properties.Entries))
	for _, entry := range child.Properties.Entries {
		seenProps[entry.XMLName.Local] = struct{}{}
	}
	for _, entry := range parent.Properties.Entries {
		if _, exists := seenProps[entry.XMLName.Local]; exists {
			continue
		}
		merged.Properties.Entries = append(merged.Properties.Entries, entry)
	}
	merged.Properties.resolve()
	merged.Dependencies = append(append([]pomArtifact{}, parent.Dependencies...), child.Dependencies...)
	merged.Plugins = mergePlugins(parent.Plugins, child.Plugins)
	merged.PluginManagement = mergePlugins(parent.PluginManagement, child.PluginManagement)
	if merged.Parent.ArtifactID == "" {
		merged.Parent = parent.Parent
	}
	return merged
}

func stampPlugin(plugin *pomPlugin, source string) {
	plugin.Source = source
	if strings.TrimSpace(plugin.Configuration.Release) != "" && plugin.ReleaseSource == "" {
		plugin.ReleaseSource = source
	}
	if strings.TrimSpace(plugin.Configuration.Source) != "" && plugin.SourceSource == "" {
		plugin.SourceSource = source
	}
	if strings.TrimSpace(plugin.Configuration.Target) != "" && plugin.TargetSource == "" {
		plugin.TargetSource = source
	}
}

func pluginInherited(plugin pomPlugin) bool {
	return !strings.EqualFold(strings.TrimSpace(plugin.Inherited), "false")
}

func mergePlugins(parent, child []pomPlugin) []pomPlugin {
	var merged []pomPlugin
	for _, item := range parent {
		if !pluginInherited(item) {
			continue
		}
		merged = append(merged, item)
	}
	for _, item := range child {
		replaced := false
		for i, existing := range merged {
			if pluginsMatch(existing, item) {
				merged[i] = mergePlugin(existing, item)
				replaced = true
				break
			}
		}
		if !replaced {
			merged = append(merged, item)
		}
	}
	return merged
}

func mergePlugin(parent, child pomPlugin) pomPlugin {
	merged := child
	if strings.TrimSpace(merged.Configuration.Release) == "" {
		merged.Configuration.Release = parent.Configuration.Release
		merged.ReleaseSource = firstNonEmpty(parent.ReleaseSource, parent.Source)
	}
	if strings.TrimSpace(merged.Configuration.Source) == "" {
		merged.Configuration.Source = parent.Configuration.Source
		merged.SourceSource = firstNonEmpty(parent.SourceSource, parent.Source)
	}
	if strings.TrimSpace(merged.Configuration.Target) == "" {
		merged.Configuration.Target = parent.Configuration.Target
		merged.TargetSource = firstNonEmpty(parent.TargetSource, parent.Source)
	}
	return merged
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func pluginsMatch(a, b pomPlugin) bool {
	if strings.TrimSpace(a.ArtifactID) != strings.TrimSpace(b.ArtifactID) {
		return false
	}
	groupA, groupB := strings.TrimSpace(a.GroupID), strings.TrimSpace(b.GroupID)
	if groupA == "" || groupB == "" {
		return true
	}
	return groupA == groupB
}

func pomJavaVersion(parsed pomDocument) (string, string, string) {
	values := propertyMap(parsed.Properties)
	if version, pointer, source, ok := compilerPluginJavaVersion(parsed, parsed.Plugins, values, "/build/plugins/maven-compiler-plugin"); ok {
		return version, pointer, source
	}
	if version, pointer, source, ok := compilerPluginJavaVersion(parsed, parsed.PluginManagement, values, "/build/pluginManagement/plugins/maven-compiler-plugin"); ok {
		return version, pointer, source
	}
	for _, name := range []string{"java.version", "maven.compiler.release", "maven.compiler.source", "maven.compiler.target"} {
		if version := literalVersion(parsed.Properties.lookup(name)); version != "" {
			return version, "/properties/" + pointerToken(name), propertySource(parsed, name)
		}
	}
	return "", "", ""
}

func compilerPluginJavaVersion(parsed pomDocument, plugins []pomPlugin, values map[string]string, pointerBase string) (string, string, string, bool) {
	for _, plugin := range plugins {
		if !isCompilerPlugin(plugin) {
			continue
		}
		for _, candidate := range []struct {
			value  string
			field  string
			source string
		}{
			{plugin.Configuration.Release, "release", plugin.ReleaseSource},
			{plugin.Configuration.Source, "source", plugin.SourceSource},
			{plugin.Configuration.Target, "target", plugin.TargetSource},
		} {
			raw := strings.TrimSpace(candidate.value)
			if raw == "" {
				continue
			}
			if name := singlePropertyRef(raw); name != "" {
				if version := literalVersion(interpolate(raw, values, 0)); version != "" {
					return version, "/properties/" + pointerToken(name), propertySource(parsed, name), true
				}
			}
			if version := literalVersion(interpolate(raw, values, 0)); version != "" {
				source := firstNonEmpty(candidate.source, plugin.Source, parsed.Source)
				return version, pointerBase + "/configuration/" + candidate.field, source, true
			}
		}
	}
	return "", "", "", false
}

func singlePropertyRef(value string) string {
	match := propertyPattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(match) != 2 || match[0] != strings.TrimSpace(value) {
		return ""
	}
	return match[1]
}

func propertySource(parsed pomDocument, name string) string {
	for _, entry := range parsed.Properties.Entries {
		if entry.XMLName.Local == name && entry.Source != "" {
			return entry.Source
		}
	}
	if parsed.Source != "" {
		return parsed.Source
	}
	return "pom.xml"
}

func propertyMap(properties pomProperties) map[string]string {
	values := make(map[string]string, len(properties.Entries))
	for _, entry := range properties.Entries {
		values[entry.XMLName.Local] = strings.TrimSpace(entry.Value)
	}
	return values
}

func literalVersion(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "${") {
		return ""
	}
	return value
}

func isCompilerPlugin(plugin pomPlugin) bool {
	return plugin.ArtifactID == "maven-compiler-plugin"
}

func pomSpringBoot(parsed pomDocument) (bool, string, string) {
	if parsed.Parent.ArtifactID == "spring-boot-starter-parent" {
		return true, "/parent/artifactId", parsed.Source
	}
	for _, dep := range parsed.Dependencies {
		if isSpringBootArtifact(dep.GroupID, dep.ArtifactID) {
			source := dep.Source
			if source == "" {
				source = parsed.Source
			}
			return true, "/dependencies/" + pointerToken(dep.ArtifactID), source
		}
	}
	for _, plugin := range parsed.Plugins {
		if plugin.ArtifactID == "spring-boot-maven-plugin" {
			source := plugin.Source
			if source == "" {
				source = parsed.Source
			}
			return true, "/build/plugins/" + pointerToken(plugin.ArtifactID), source
		}
	}
	return false, "", ""
}

func isSpringBootArtifact(groupID, artifactID string) bool {
	if groupID != "" && groupID != "org.springframework.boot" {
		return false
	}
	return artifactID == "spring-boot-starter-parent" || strings.HasPrefix(artifactID, "spring-boot-starter")
}

func pluginSet(parsed pomDocument) map[string]string {
	out := make(map[string]string)
	for _, plugin := range parsed.Plugins {
		name := strings.TrimSpace(plugin.ArtifactID)
		if name == "" {
			continue
		}
		source := plugin.Source
		if source == "" {
			source = parsed.Source
		}
		out[name] = source
	}
	return out
}

func (m *mavenProject) springBootEvidence(ctx provider.Context) []plan.Evidence {
	if m == nil || !m.SpringBoot {
		return nil
	}
	source := m.SpringSource
	if source == "" {
		source = ctx.SourcePath(m.Source)
	}
	return []plan.Evidence{{
		Kind:        plan.EvidenceDeclaration,
		Source:      source,
		Pointer:     m.SpringPointer,
		Description: "The Maven project declares Spring Boot.",
	}}
}

func (m *mavenProject) pluginSource(artifactID string) string {
	if m == nil {
		return ""
	}
	return m.Plugins[artifactID]
}

func (m *mavenProject) hasSpringBootPlugin() bool {
	return m.pluginSource("spring-boot-maven-plugin") != ""
}

func mavenWrapper(ctx provider.Context) (script, source, version, properties string) {
	dir := ctx.ProjectDir()
	for {
		name := ""
		switch {
		case fileExists(dir, "mvnw"):
			name = "mvnw"
		case fileExists(dir, "mvnw.cmd"):
			name = "mvnw.cmd"
		}
		if name != "" {
			script = wrapperRun(ctx.ProjectDir(), filepath.Join(dir, name))
			if rel, err := filepath.Rel(ctx.RepositoryRoot, filepath.Join(dir, name)); err == nil {
				source = filepath.ToSlash(rel)
			}
			propName := ".mvn/wrapper/maven-wrapper.properties"
			if fileExists(dir, propName) {
				if rel, err := filepath.Rel(ctx.RepositoryRoot, filepath.Join(dir, filepath.FromSlash(propName))); err == nil {
					properties = filepath.ToSlash(rel)
				}
				version = wrapperVersionFromURL(readFileIfPresent(dir, propName), "apache-maven-")
			}
			return script, source, version, properties
		}
		if dir == ctx.RepositoryRoot {
			return "", "", "", ""
		}
		parent := filepath.Dir(dir)
		if parent == dir || !insideRepository(ctx.RepositoryRoot, parent) {
			return "", "", "", ""
		}
		dir = parent
	}
}

func wrapperRun(fromDir, wrapperPath string) string {
	rel, err := filepath.Rel(fromDir, wrapperPath)
	if err != nil {
		return ""
	}
	run := filepath.ToSlash(rel)
	if path.Base(run) == "mvnw" && !strings.HasPrefix(run, "../") {
		return "./" + strings.TrimPrefix(run, "./")
	}
	return run
}

func insideRepository(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}
