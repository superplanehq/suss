package compose

import (
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
	return extractCompose(yamlToSourced(root, source, "")), nil
}

func mergeCompose(base, override composeFile) composeFile {
	merged := extractCompose(mergeSourced(base.Root, override.Root, nil))
	merged.HasInclude = base.HasInclude || override.HasInclude
	return merged
}

func extractCompose(root sourcedNode) composeFile {
	doc := unwrapDocument(root)
	services := map[string]composeService{}
	for _, pair := range mappingPairs(mappingValue(doc, "services")) {
		name := strings.TrimSpace(pair.key.Value)
		if name == "" {
			continue
		}
		services[name] = serviceFromNode(pair.value)
	}
	return composeFile{
		HasInclude:     !isZeroSourced(mappingValue(doc, "include")),
		Services:       services,
		Interpolations: interpolationsFromSourced(root, ""),
		Root:           root,
	}
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
	seen := make(map[string]int)
	for _, pair := range mappingPairs(node) {
		name := strings.TrimSpace(pair.key.Value)
		if !validEnvName(name) {
			continue
		}
		item := envVar{
			Name:       name,
			HasDefault: sourcedScalar(pair.value) != "",
			Source:     firstNonEmpty(pair.value.Source, pair.key.Source),
		}
		if idx, exists := seen[name]; exists {
			out[idx] = item
			continue
		}
		seen[name] = len(out)
		out = append(out, item)
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
		out = append(out, envVar{Name: name, HasDefault: hasDefault, Source: item.Source})
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
