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
	Root           yaml.Node
}

type composeService struct {
	Image       string
	Environment []envVar
}

type envVar struct {
	Name       string
	HasDefault bool
}

type locatedVar struct {
	Name       string
	HasDefault bool
	Pointer    string
}

func parseCompose(contents []byte) (composeFile, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(contents, &root); err != nil {
		return composeFile{}, err
	}
	return composeFromDocument(root)
}

func composeFromDocument(root yaml.Node) (composeFile, error) {
	var raw struct {
		Include  yaml.Node                 `yaml:"include"`
		Services map[string]composeService `yaml:"services"`
	}
	if err := root.Decode(&raw); err != nil {
		return composeFile{}, err
	}
	if raw.Services == nil {
		raw.Services = map[string]composeService{}
	}
	return composeFile{
		HasInclude:     !isZeroNode(raw.Include),
		Services:       raw.Services,
		Interpolations: interpolationsFromNode(root, ""),
		Root:           root,
	}, nil
}

func mergeCompose(base, override composeFile) (composeFile, error) {
	merged, err := composeFromDocument(mergeNodes(base.Root, override.Root))
	if err != nil {
		return composeFile{}, err
	}
	merged.HasInclude = base.HasInclude || override.HasInclude
	return merged, nil
}

func (s *composeService) UnmarshalYAML(value *yaml.Node) error {
	var raw struct {
		Image       string    `yaml:"image"`
		Environment yaml.Node `yaml:"environment"`
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	s.Image = raw.Image
	s.Environment = parseEnvironment(raw.Environment)
	return nil
}

func interpolationsFromNode(node yaml.Node, pointer string) []locatedVar {
	node = resolveNode(node)
	switch node.Kind {
	case yaml.DocumentNode:
		if len(node.Content) == 0 {
			return nil
		}
		return interpolationsFromNode(*node.Content[0], pointer)
	case yaml.MappingNode:
		var out []locatedVar
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := resolveNode(*node.Content[i])
			value := resolveNode(*node.Content[i+1])
			name := strings.TrimSpace(key.Value)
			if name == "<<" {
				out = append(out, interpolationsFromNode(value, pointer)...)
				continue
			}
			child := pointer
			if name != "" {
				child += jsonPointer(name)
			}
			if key.Kind == yaml.ScalarNode {
				out = append(out, locateVars(child, interpolationsFrom(key.Value))...)
			}
			out = append(out, interpolationsFromNode(value, child)...)
		}
		return out
	case yaml.SequenceNode:
		var out []locatedVar
		for i, item := range node.Content {
			out = append(out, interpolationsFromNode(*item, pointer+jsonPointer(strconv.Itoa(i)))...)
		}
		return out
	case yaml.ScalarNode:
		return locateVars(pointer, interpolationsFrom(node.Value))
	default:
		return nil
	}
}

func locateVars(pointer string, vars []envVar) []locatedVar {
	out := make([]locatedVar, 0, len(vars))
	for _, item := range vars {
		out = append(out, locatedVar{Name: item.Name, HasDefault: item.HasDefault, Pointer: pointer})
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

func parseEnvironment(node yaml.Node) []envVar {
	node = resolveNode(node)
	if isZeroNode(node) {
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

func environmentFromMap(node yaml.Node) []envVar {
	var out []envVar
	seen := make(map[string]int)
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := resolveNode(*node.Content[i])
		value := resolveNode(*node.Content[i+1])
		name := strings.TrimSpace(key.Value)
		if name == "<<" {
			for _, item := range mergeEnvironment(value) {
				if _, exists := seen[item.Name]; exists {
					continue
				}
				seen[item.Name] = len(out)
				out = append(out, item)
			}
			continue
		}
		if !validEnvName(name) {
			continue
		}
		item := envVar{Name: name, HasDefault: scalarString(&value) != ""}
		if idx, exists := seen[name]; exists {
			out[idx] = item
			continue
		}
		seen[name] = len(out)
		out = append(out, item)
	}
	return out
}

func mergeEnvironment(node yaml.Node) []envVar {
	node = resolveNode(node)
	switch node.Kind {
	case yaml.MappingNode:
		return parseEnvironment(node)
	case yaml.SequenceNode:
		var out []envVar
		for _, item := range node.Content {
			out = append(out, parseEnvironment(*item)...)
		}
		return out
	default:
		return nil
	}
}

func environmentFromList(node yaml.Node) []envVar {
	var out []envVar
	for _, item := range node.Content {
		resolved := resolveNode(*item)
		if resolved.Kind != yaml.ScalarNode || resolved.Tag == "!!null" {
			continue
		}
		name, value, found := strings.Cut(resolved.Value, "=")
		name = strings.TrimSpace(name)
		if !validEnvName(name) {
			continue
		}
		hasDefault := found && strings.TrimSpace(value) != ""
		out = append(out, envVar{Name: name, HasDefault: hasDefault})
	}
	return out
}

func resolveNode(node yaml.Node) yaml.Node {
	if node.Kind == yaml.AliasNode && node.Alias != nil {
		return resolveNode(*node.Alias)
	}
	return node
}

func scalarString(node *yaml.Node) string {
	if node == nil {
		return ""
	}
	resolved := resolveNode(*node)
	if resolved.Kind != yaml.ScalarNode || resolved.Tag == "!!null" {
		return ""
	}
	return resolved.Value
}

func isZeroNode(node yaml.Node) bool {
	return node.Kind == 0 && node.Tag == "" && node.Value == "" && len(node.Content) == 0
}

func mergeNodes(base, override yaml.Node) yaml.Node {
	base = resolveNode(base)
	override = resolveNode(override)
	if isZeroNode(override) {
		return cloneNode(base)
	}
	if isZeroNode(base) {
		return cloneNode(override)
	}
	if base.Kind == yaml.DocumentNode || override.Kind == yaml.DocumentNode {
		return mergeDocuments(base, override)
	}
	if base.Kind == yaml.MappingNode && override.Kind == yaml.MappingNode {
		return mergeMappings(base, override)
	}
	return cloneNode(override)
}

func mergeDocuments(base, override yaml.Node) yaml.Node {
	baseRoot, baseOK := documentRoot(base)
	overrideRoot, overrideOK := documentRoot(override)
	if !baseOK {
		return cloneNode(override)
	}
	if !overrideOK {
		return cloneNode(base)
	}
	merged := mergeNodes(baseRoot, overrideRoot)
	return yaml.Node{
		Kind:    yaml.DocumentNode,
		Content: []*yaml.Node{&merged},
	}
}

func documentRoot(node yaml.Node) (yaml.Node, bool) {
	node = resolveNode(node)
	if node.Kind != yaml.DocumentNode {
		return node, true
	}
	if len(node.Content) == 0 {
		return yaml.Node{}, false
	}
	return resolveNode(*node.Content[0]), true
}

func mergeMappings(base, override yaml.Node) yaml.Node {
	out := yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	index := make(map[string]int)
	for i := 0; i+1 < len(base.Content); i += 2 {
		key := cloneNode(resolveNode(*base.Content[i]))
		value := cloneNode(resolveNode(*base.Content[i+1]))
		index[strings.TrimSpace(key.Value)] = len(out.Content)
		out.Content = append(out.Content, &key, &value)
	}
	for i := 0; i+1 < len(override.Content); i += 2 {
		keyNode := resolveNode(*override.Content[i])
		valueNode := resolveNode(*override.Content[i+1])
		name := strings.TrimSpace(keyNode.Value)
		if pos, exists := index[name]; exists {
			merged := mergeMappingValue(name, *out.Content[pos+1], valueNode)
			out.Content[pos+1] = &merged
			continue
		}
		key := cloneNode(keyNode)
		value := cloneNode(valueNode)
		index[name] = len(out.Content)
		out.Content = append(out.Content, &key, &value)
	}
	return out
}

func mergeMappingValue(key string, base, override yaml.Node) yaml.Node {
	if key == "environment" || key == "labels" {
		return mergeNodes(keyValueMapping(base), keyValueMapping(override))
	}
	return mergeNodes(base, override)
}

func keyValueMapping(node yaml.Node) yaml.Node {
	node = resolveNode(node)
	if node.Kind == yaml.MappingNode {
		return node
	}
	out := yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	if node.Kind != yaml.SequenceNode {
		return out
	}
	for _, item := range node.Content {
		resolved := resolveNode(*item)
		if resolved.Kind != yaml.ScalarNode {
			continue
		}
		name, value, found := strings.Cut(resolved.Value, "=")
		name = strings.TrimSpace(name)
		key := yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name}
		val := yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null"}
		if found {
			val.Tag = "!!str"
			val.Value = value
		}
		out.Content = append(out.Content, &key, &val)
	}
	return out
}

func cloneNode(node yaml.Node) yaml.Node {
	node = resolveNode(node)
	out := yaml.Node{
		Kind:        node.Kind,
		Style:       node.Style,
		Tag:         node.Tag,
		Value:       node.Value,
		Anchor:      node.Anchor,
		HeadComment: node.HeadComment,
		LineComment: node.LineComment,
		FootComment: node.FootComment,
		Line:        node.Line,
		Column:      node.Column,
	}
	if len(node.Content) == 0 {
		return out
	}
	out.Content = make([]*yaml.Node, len(node.Content))
	for i, child := range node.Content {
		if child == nil {
			continue
		}
		cloned := cloneNode(*child)
		out.Content[i] = &cloned
	}
	return out
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
