package compose

import (
	"strings"

	"gopkg.in/yaml.v3"
)

type composeFile struct {
	HasInclude bool
	Services   map[string]composeService
}

type composeService struct {
	Image       string
	Environment []envVar
}

type envVar struct {
	Name       string
	HasDefault bool
}

func parseCompose(contents []byte) (composeFile, error) {
	var raw struct {
		Include  yaml.Node                 `yaml:"include"`
		Services map[string]composeService `yaml:"services"`
	}
	if err := yaml.Unmarshal(contents, &raw); err != nil {
		return composeFile{}, err
	}
	if raw.Services == nil {
		raw.Services = map[string]composeService{}
	}
	return composeFile{
		HasInclude: !isZeroNode(raw.Include),
		Services:   raw.Services,
	}, nil
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
