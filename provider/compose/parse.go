package compose

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type composeFile struct {
	HasInclude     bool
	Services       map[string]composeService
	Interpolations []locatedVar
	Root           sourcedNode
}

type composeService struct {
	Image       string
	ImageSource string
	Source      string
	Environment []envVar
}

type envVar struct {
	Name       string
	HasDefault bool
	Source     string
	Pointer    string
}

type locatedVar struct {
	Name       string
	HasDefault bool
	Pointer    string
	Source     string
}

func parseCompose(contents []byte, source string) (composeFile, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(contents, &root); err != nil {
		return composeFile{}, err
	}
	file, err := extractCompose(resolveMergeKeys(yamlToSourced(root, source, "")))
	if err != nil {
		return composeFile{}, err
	}
	file.Interpolations = interpolationsFromSourced(file.Root, "")
	return file, nil
}

func mergeCompose(base, override composeFile) (composeFile, error) {
	merged, err := extractCompose(resolveMergeKeys(mergeSourced(base.Root, override.Root, nil)))
	if err != nil {
		return composeFile{}, err
	}
	merged.HasInclude = base.HasInclude || override.HasInclude
	merged.Interpolations = append(append([]locatedVar{}, base.Interpolations...), override.Interpolations...)
	return merged, nil
}

func extractCompose(root sourcedNode) (composeFile, error) {
	doc := unwrapDocument(root)
	if err := validateCompose(doc); err != nil {
		return composeFile{}, err
	}
	services := map[string]composeService{}
	for _, pair := range mappingPairs(mappingValue(doc, "services")) {
		name := strings.TrimSpace(pair.key.Value)
		if name == "" {
			continue
		}
		services[name] = serviceFromNode(pair.value)
	}
	return composeFile{
		HasInclude: !isZeroSourced(mappingValue(doc, "include")),
		Services:   services,
		Root:       root,
	}, nil
}

func validateCompose(doc sourcedNode) error {
	if isZeroSourced(doc) {
		return nil
	}
	if doc.Kind != yaml.MappingNode {
		return fmt.Errorf("compose file must be a mapping")
	}
	services := mappingValue(doc, "services")
	if isZeroSourced(services) {
		return nil
	}
	if services.Kind != yaml.MappingNode {
		return fmt.Errorf("services must be a mapping")
	}
	for _, pair := range mappingPairs(services) {
		name := strings.TrimSpace(pair.key.Value)
		if isZeroSourced(pair.value) {
			continue
		}
		if pair.value.Kind != yaml.MappingNode {
			return fmt.Errorf("service %s must be a mapping", name)
		}
		if err := validateServiceFields(name, pair.value); err != nil {
			return err
		}
	}
	return nil
}

func validateServiceFields(name string, service sourcedNode) error {
	if err := validateImageField(name, mappingValue(service, "image")); err != nil {
		return err
	}
	return validateEnvironmentField(name, mappingValue(service, "environment"))
}

func validateImageField(name string, image sourcedNode) error {
	if isAbsentOrReset(image) {
		return nil
	}
	if image.Kind != yaml.ScalarNode {
		return fmt.Errorf("service %s image must be a string", name)
	}
	return nil
}

func validateEnvironmentField(name string, environment sourcedNode) error {
	if isAbsentOrReset(environment) {
		return nil
	}
	if environment.Kind != yaml.MappingNode && environment.Kind != yaml.SequenceNode {
		return fmt.Errorf("service %s environment must be a mapping or sequence", name)
	}
	return nil
}

func isAbsentOrReset(node sourcedNode) bool {
	return isZeroSourced(node) || isReset(node)
}

func serviceFromNode(node sourcedNode) composeService {
	image := mappingValue(node, "image")
	svc := composeService{Source: node.Source}
	if image.Kind == yaml.ScalarNode && image.Tag != "!!null" {
		svc.Image = image.Value
		svc.ImageSource = image.Source
	}
	svc.Environment = parseEnvironment(mappingValue(node, "environment"))
	return svc
}

func interpolationsFromSourced(node sourcedNode, pointer string) []locatedVar {
	switch node.Kind {
	case yaml.DocumentNode:
		if len(node.Content) == 0 {
			return nil
		}
		return interpolationsFromSourced(node.Content[0], pointer)
	case yaml.MappingNode:
		node = flattenMapping(node)
		var out []locatedVar
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i]
			value := node.Content[i+1]
			name := strings.TrimSpace(key.Value)
			child := value.Pointer
			if child == "" {
				child = pointer
				if name != "" && name != "<<" {
					child += jsonPointer(name)
				}
			}
			out = append(out, interpolationsFromSourced(value, child)...)
		}
		return out
	case yaml.SequenceNode:
		var out []locatedVar
		for i, item := range node.Content {
			child := item.Pointer
			if child == "" {
				child = pointer + jsonPointer(strconv.Itoa(i))
			}
			out = append(out, interpolationsFromSourced(item, child)...)
		}
		return out
	case yaml.ScalarNode:
		if node.Style == yaml.SingleQuotedStyle {
			return nil
		}
		if node.Pointer != "" {
			pointer = node.Pointer
		}
		return locateVars(node.Source, pointer, interpolationsFrom(node.Value))
	default:
		return nil
	}
}

func locateVars(source, pointer string, vars []envVar) []locatedVar {
	out := make([]locatedVar, 0, len(vars))
	for _, item := range vars {
		out = append(out, locatedVar{
			Name:       item.Name,
			HasDefault: item.HasDefault,
			Pointer:    pointer,
			Source:     source,
		})
	}
	return out
}

func interpolationsFrom(s string) []envVar {
	var out []envVar
	seen := make(map[string]int)
	for i := 0; i < len(s); {
		if s[i] != '$' {
			i++
			continue
		}
		if i+1 < len(s) && s[i+1] == '$' {
			i += 2
			continue
		}
		name, next, hasDefault, ok := readInterpolation(s, i)
		if !ok {
			i++
			continue
		}
		if validEnvName(name) {
			item := envVar{Name: name, HasDefault: hasDefault}
			if idx, exists := seen[name]; exists {
				out[idx].HasDefault = out[idx].HasDefault || hasDefault
			} else {
				seen[name] = len(out)
				out = append(out, item)
			}
		}
		if s[i+1] == '{' && next > i+3 {
			for _, nested := range interpolationsFrom(s[i+2 : next-1]) {
				if idx, exists := seen[nested.Name]; exists {
					out[idx].HasDefault = out[idx].HasDefault || nested.HasDefault
					continue
				}
				seen[nested.Name] = len(out)
				out = append(out, nested)
			}
		}
		i = next
	}
	return out
}

func readInterpolation(s string, i int) (name string, end int, hasDefault bool, ok bool) {
	if i+1 >= len(s) {
		return "", 0, false, false
	}
	if s[i+1] == '{' {
		return readBraceInterpolation(s, i+2)
	}
	if !interpNameStart(s[i+1]) {
		return "", 0, false, false
	}
	j := i + 2
	for j < len(s) && interpNamePart(s[j], j > i+1) {
		j++
	}
	return s[i+1 : j], j, false, true
}

func readBraceInterpolation(s string, start int) (name string, end int, hasDefault bool, ok bool) {
	j := start
	for j < len(s) && interpNamePart(s[j], j > start) {
		j++
	}
	if j == start {
		return "", 0, false, false
	}
	name = s[start:j]
	if j < len(s) && s[j] == '}' {
		return name, j + 1, false, true
	}
	if j >= len(s) {
		return "", 0, false, false
	}
	switch s[j] {
	case ':':
		if j+1 < len(s) && (s[j+1] == '-' || s[j+1] == '=') {
			hasDefault = true
		}
	case '-', '=':
		hasDefault = true
	case '+', '?':
	default:
		return "", 0, false, false
	}
	depth := 1
	for k := j; k < len(s); k++ {
		switch s[k] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return name, k + 1, hasDefault, true
			}
		}
	}
	return "", 0, false, false
}

func interpNameStart(b byte) bool {
	return b == '_' || (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

func interpNamePart(b byte, allowDigit bool) bool {
	if interpNameStart(b) {
		return true
	}
	return allowDigit && b >= '0' && b <= '9'
}

func parseEnvironment(node sourcedNode) []envVar {
	if isZeroSourced(node) {
		return nil
	}
	switch node.Kind {
	case yaml.MappingNode:
		return environmentFromMap(node)
	case yaml.SequenceNode:
		return environmentFromList(node)
	default:
		return nil
	}
}

func environmentFromMap(node sourcedNode) []envVar {
	var out []envVar
	for _, pair := range mappingPairs(node) {
		name := strings.TrimSpace(pair.key.Value)
		if !validEnvName(name) {
			continue
		}
		out = append(out, envVar{
			Name:       name,
			HasDefault: sourcedScalar(pair.value) != "",
			Source:     firstNonEmpty(pair.value.Source, pair.key.Source),
			Pointer:    firstNonEmpty(pair.value.Pointer, pair.key.Pointer),
		})
	}
	return out
}

func environmentFromList(node sourcedNode) []envVar {
	var out []envVar
	for _, item := range node.Content {
		if item.Kind != yaml.ScalarNode || item.Tag == "!!null" {
			continue
		}
		name, value, found := strings.Cut(item.Value, "=")
		name = strings.TrimSpace(name)
		if !validEnvName(name) {
			continue
		}
		hasDefault := found && strings.TrimSpace(value) != ""
		out = append(out, envVar{Name: name, HasDefault: hasDefault, Source: item.Source, Pointer: item.Pointer})
	}
	return out
}

func sourcedScalar(node sourcedNode) string {
	if node.Kind != yaml.ScalarNode || node.Tag == "!!null" {
		return ""
	}
	return node.Value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func validEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
			continue
		}
		if r >= '0' && r <= '9' && i > 0 {
			continue
		}
		return false
	}
	return true
}
