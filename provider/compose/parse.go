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
	out := make([]envVar, 0, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		name := strings.TrimSpace(node.Content[i].Value)
		if !validEnvName(name) {
			continue
		}
		value := scalarString(node.Content[i+1])
		out = append(out, envVar{Name: name, HasDefault: value != ""})
	}
	return out
}

func environmentFromList(node yaml.Node) []envVar {
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
		out = append(out, envVar{Name: name, HasDefault: hasDefault})
	}
	return out
}

func scalarString(node *yaml.Node) string {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag == "!!null" {
		return ""
	}
	return node.Value
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
