package java

import (
	"encoding/xml"
	"fmt"
	"os"
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
	Aggregator        bool
	SpringBoot        bool
	SpringPointer     string
	Plugins           map[string]struct{}
	Wrapper           string
	WrapperVersion    string
	WrapperProperties string
}

type pomDocument struct {
	XMLName          xml.Name      `xml:"project"`
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
	RelativePath *string `xml:"relativePath"`
}

type pomArtifact struct {
	GroupID    string `xml:"groupId"`
	ArtifactID string `xml:"artifactId"`
}

type pomPlugin struct {
	GroupID       string `xml:"groupId"`
	ArtifactID    string `xml:"artifactId"`
	Configuration struct {
		Release string `xml:"release"`
		Source  string `xml:"source"`
		Target  string `xml:"target"`
	} `xml:"configuration"`
}

type pomProperties struct {
	Entries []pomProperty `xml:",any"`
}

type pomProperty struct {
	XMLName xml.Name
	Value   string `xml:",chardata"`
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
		Plugins:    pluginSet(parsed),
	}
	project.JavaVersion, project.JavaVersionPtr = pomJavaVersion(parsed)
	project.SpringBoot, project.SpringPointer = pomSpringBoot(parsed)
	project.Wrapper, project.WrapperVersion, project.WrapperProperties = mavenWrapper(ctx.ProjectDir())
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
	return loadPOMFile(abs, repoRoot, seen)
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

func mergePOM(parent, child pomDocument) pomDocument {
	merged := child
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
	merged.Plugins = append(append([]pomPlugin{}, parent.Plugins...), child.Plugins...)
	merged.PluginManagement = append(append([]pomPlugin{}, parent.PluginManagement...), child.PluginManagement...)
	if merged.Parent.ArtifactID == "" {
		merged.Parent = parent.Parent
	}
	return merged
}

func pomJavaVersion(parsed pomDocument) (string, string) {
	for _, name := range []string{"java.version", "maven.compiler.release", "maven.compiler.source", "maven.compiler.target"} {
		if version := literalVersion(parsed.Properties.lookup(name)); version != "" {
			return version, "/properties/" + pointerToken(name)
		}
	}
	values := propertyMap(parsed.Properties)
	for _, plugin := range append(append([]pomPlugin{}, parsed.Plugins...), parsed.PluginManagement...) {
		if !isCompilerPlugin(plugin) {
			continue
		}
		for _, candidate := range []struct {
			value   string
			pointer string
		}{
			{plugin.Configuration.Release, "/build/plugins/maven-compiler-plugin/configuration/release"},
			{plugin.Configuration.Source, "/build/plugins/maven-compiler-plugin/configuration/source"},
			{plugin.Configuration.Target, "/build/plugins/maven-compiler-plugin/configuration/target"},
		} {
			if version := literalVersion(interpolate(candidate.value, values, 0)); version != "" {
				return version, candidate.pointer
			}
		}
	}
	return "", ""
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

func pomSpringBoot(parsed pomDocument) (bool, string) {
	if parsed.Parent.ArtifactID == "spring-boot-starter-parent" {
		return true, "/parent/artifactId"
	}
	for _, dep := range parsed.Dependencies {
		if isSpringBootArtifact(dep.GroupID, dep.ArtifactID) {
			return true, "/dependencies/" + pointerToken(dep.ArtifactID)
		}
	}
	for _, plugin := range append(append([]pomPlugin{}, parsed.Plugins...), parsed.PluginManagement...) {
		if plugin.ArtifactID == "spring-boot-maven-plugin" {
			return true, "/build/plugins/" + pointerToken(plugin.ArtifactID)
		}
	}
	return false, ""
}

func isSpringBootArtifact(groupID, artifactID string) bool {
	if groupID != "" && groupID != "org.springframework.boot" {
		return false
	}
	return artifactID == "spring-boot-starter-parent" || strings.HasPrefix(artifactID, "spring-boot-starter")
}

func pluginSet(parsed pomDocument) map[string]struct{} {
	out := make(map[string]struct{})
	for _, plugin := range append(append([]pomPlugin{}, parsed.Plugins...), parsed.PluginManagement...) {
		name := strings.TrimSpace(plugin.ArtifactID)
		if name != "" {
			out[name] = struct{}{}
		}
	}
	return out
}

func (m *mavenProject) springBootEvidence(ctx provider.Context) []plan.Evidence {
	if m == nil || !m.SpringBoot {
		return nil
	}
	return []plan.Evidence{{
		Kind:        plan.EvidenceDeclaration,
		Source:      ctx.SourcePath(m.Source),
		Pointer:     m.SpringPointer,
		Description: "The Maven project declares Spring Boot.",
	}}
}

func (m *mavenProject) hasPlugin(artifactID string) bool {
	if m == nil {
		return false
	}
	_, ok := m.Plugins[artifactID]
	return ok
}

func mavenWrapper(dir string) (script, version, properties string) {
	if fileExists(dir, "mvnw") {
		script = "./mvnw"
	} else if fileExists(dir, "mvnw.cmd") {
		script = "mvnw.cmd"
	}
	name := ".mvn/wrapper/maven-wrapper.properties"
	if fileExists(dir, name) {
		properties = name
		version = wrapperVersionFromURL(readFileIfPresent(dir, name), "apache-maven-")
	}
	return script, version, properties
}

func insideRepository(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}
