package gha

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

type workflowFile struct {
	Env      stringMap      `yaml:"env"`
	Defaults *defaultsBlock `yaml:"defaults"`
	Jobs     map[string]job `yaml:"jobs"`
}

type defaultsBlock struct {
	Run *runDefaults `yaml:"run"`
}

type runDefaults struct {
	WorkingDirectory string `yaml:"working-directory"`
}

type job struct {
	Uses     string             `yaml:"uses"`
	Defaults *defaultsBlock     `yaml:"defaults"`
	Env      stringMap          `yaml:"env"`
	Services map[string]service `yaml:"services"`
	Strategy *strategy          `yaml:"strategy"`
	Steps    []step             `yaml:"steps"`
}

type strategy struct {
	Matrix yaml.Node `yaml:"matrix"`
}

type service struct {
	Image string    `yaml:"image"`
	Env   stringMap `yaml:"env"`
}

type step struct {
	Name             string    `yaml:"name"`
	Uses             string    `yaml:"uses"`
	Run              string    `yaml:"run"`
	WorkingDirectory string    `yaml:"working-directory"`
	Env              stringMap `yaml:"env"`
	With             stringMap `yaml:"with"`
}

type stringMap map[string]string

func parseWorkflow(contents []byte) (workflowFile, error) {
	var workflow workflowFile
	if err := yaml.Unmarshal(contents, &workflow); err != nil {
		return workflowFile{}, err
	}
	return workflow, nil
}

func (m *stringMap) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode && (value.Tag == "!!null" || value.Value == "") {
		return nil
	}
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("expected a mapping, got %s", value.Tag)
	}

	out := make(stringMap, len(value.Content)/2)
	for i := 0; i+1 < len(value.Content); i += 2 {
		key := value.Content[i].Value
		out[key] = scalarString(value.Content[i+1])
	}
	*m = out
	return nil
}

func scalarString(node *yaml.Node) string {
	if node == nil {
		return ""
	}
	switch node.Kind {
	case yaml.ScalarNode:
		if node.Tag == "!!null" {
			return ""
		}
		return node.Value
	default:
		return strings.TrimSpace(node.Value)
	}
}

func matrixValues(node yaml.Node) map[string][]string {
	if node.Kind != yaml.MappingNode {
		return nil
	}
	out := make(map[string][]string)
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		value := node.Content[i+1]
		if key == "include" {
			collectInclude(value, out)
			continue
		}
		if key == "exclude" {
			continue
		}
		out[key] = appendUnique(out[key], sequenceStrings(value)...)
	}
	return out
}

func collectInclude(node *yaml.Node, out map[string][]string) {
	if node == nil || node.Kind != yaml.SequenceNode {
		return
	}
	for _, item := range node.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		for i := 0; i+1 < len(item.Content); i += 2 {
			key := item.Content[i].Value
			out[key] = appendUnique(out[key], scalarString(item.Content[i+1]))
		}
	}
}

func sequenceStrings(node *yaml.Node) []string {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.ScalarNode {
		if node.Tag == "!!null" || node.Value == "" {
			return nil
		}
		return []string{node.Value}
	}
	if node.Kind != yaml.SequenceNode {
		return nil
	}
	var values []string
	for _, item := range node.Content {
		if item.Kind == yaml.ScalarNode && item.Tag != "!!null" && item.Value != "" {
			values = append(values, item.Value)
		}
	}
	return values
}

func appendUnique(existing []string, values ...string) []string {
	seen := make(map[string]struct{}, len(existing))
	for _, value := range existing {
		seen[value] = struct{}{}
	}
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		existing = append(existing, value)
	}
	return existing
}
