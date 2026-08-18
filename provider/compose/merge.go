package compose

import (
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// sourcedNode is a YAML node stamped with the Compose file and JSON
// pointer that produced it. Merging keeps those on the winning node so
// findings cite the originating location, not a reconstructed merge path.
type sourcedNode struct {
	Kind    yaml.Kind
	Style   yaml.Style
	Tag     string
	Value   string
	Source  string
	Pointer string
	Content []sourcedNode
}

type mappingPair struct {
	key   sourcedNode
	value sourcedNode
}

func yamlToSourced(node yaml.Node, source, pointer string) sourcedNode {
	node = resolveNode(node)
	out := sourcedNode{
		Kind:    node.Kind,
		Style:   node.Style,
		Tag:     node.Tag,
		Value:   node.Value,
		Source:  source,
		Pointer: pointer,
	}
	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			keyNode := resolveNode(*node.Content[i])
			name := strings.TrimSpace(keyNode.Value)
			child := pointer
			if name != "" && name != "<<" {
				child += jsonPointer(name)
			}
			out.Content = append(out.Content,
				yamlToSourced(*node.Content[i], source, child),
				yamlToSourced(*node.Content[i+1], source, child),
			)
		}
	default:
		for i, child := range node.Content {
			if child == nil {
				continue
			}
			childPointer := pointer
			if node.Kind == yaml.SequenceNode {
				childPointer += jsonPointer(strconv.Itoa(i))
			}
			out.Content = append(out.Content, yamlToSourced(*child, source, childPointer))
		}
	}
	return out
}

func resolveNode(node yaml.Node) yaml.Node {
	if node.Kind == yaml.AliasNode && node.Alias != nil {
		return resolveNode(*node.Alias)
	}
	return node
}

func mergeSourced(base, override sourcedNode, path []string) sourcedNode {
	if isReset(override) {
		return sourcedNode{}
	}
	if isOverrideTag(override) {
		return cloneSourced(stripComposeTag(override))
	}
	if isZeroSourced(override) {
		return cloneSourced(base)
	}
	if isZeroSourced(base) {
		return cloneSourced(override)
	}
	if base.Kind == yaml.DocumentNode || override.Kind == yaml.DocumentNode {
		return mergeDocuments(base, override, path)
	}
	if base.Kind == yaml.MappingNode && override.Kind == yaml.MappingNode {
		return mergeMappings(base, override, path)
	}
	if base.Kind == yaml.SequenceNode && override.Kind == yaml.SequenceNode {
		return appendSequences(base, override)
	}
	return cloneSourced(override)
}

func mergeDocuments(base, override sourcedNode, path []string) sourcedNode {
	baseRoot, baseOK := documentRoot(base)
	overrideRoot, overrideOK := documentRoot(override)
	if !baseOK {
		return cloneSourced(override)
	}
	if !overrideOK {
		return cloneSourced(base)
	}
	merged := mergeSourced(baseRoot, overrideRoot, path)
	return sourcedNode{Kind: yaml.DocumentNode, Source: base.Source, Pointer: base.Pointer, Content: []sourcedNode{merged}}
}

func documentRoot(node sourcedNode) (sourcedNode, bool) {
	if node.Kind != yaml.DocumentNode {
		return node, true
	}
	if len(node.Content) == 0 {
		return sourcedNode{}, false
	}
	return node.Content[0], true
}

func mergeMappings(base, override sourcedNode, path []string) sourcedNode {
	out := sourcedNode{Kind: yaml.MappingNode, Tag: "!!map", Source: base.Source, Pointer: base.Pointer}
	index := make(map[string]int)
	for i := 0; i+1 < len(base.Content); i += 2 {
		key := cloneSourced(base.Content[i])
		value := cloneSourced(base.Content[i+1])
		index[strings.TrimSpace(key.Value)] = len(out.Content)
		out.Content = append(out.Content, key, value)
	}
	for i := 0; i+1 < len(override.Content); i += 2 {
		key := cloneSourced(override.Content[i])
		value := cloneSourced(override.Content[i+1])
		name := strings.TrimSpace(key.Value)
		if isReset(value) {
			if pos, exists := index[name]; exists {
				removeMappingPair(&out, pos, index)
			}
			continue
		}
		if pos, exists := index[name]; exists {
			out.Content[pos+1] = mergeField(path, name, out.Content[pos+1], value)
			continue
		}
		index[name] = len(out.Content)
		out.Content = append(out.Content, key, value)
	}
	return out
}

func removeMappingPair(node *sourcedNode, pos int, index map[string]int) {
	name := strings.TrimSpace(node.Content[pos].Value)
	node.Content = append(node.Content[:pos], node.Content[pos+2:]...)
	delete(index, name)
	for key, at := range index {
		if at > pos {
			index[key] = at - 2
		}
	}
}

func mergeField(path []string, key string, base, override sourcedNode) sourcedNode {
	if isReset(override) {
		return sourcedNode{}
	}
	if isOverrideTag(override) {
		return cloneSourced(stripComposeTag(override))
	}
	child := append(append([]string{}, path...), key)
	if isKeyValueAttribute(path, key) {
		return mergeSourced(asKeyValueMapping(base), asKeyValueMapping(override), child)
	}
	if isServiceAttribute(path) {
		switch key {
		case "command", "entrypoint":
			return cloneSourced(override)
		case "ports":
			return mergeUniqueSequence(base, override, portUniqueKey, expandPort)
		case "volumes":
			return mergeUniqueSequence(base, override, resourceTargetKey, expandVolume)
		case "secrets", "configs":
			return mergeUniqueSequence(base, override, resourceTargetKey, expandNamedResource)
		}
	}
	if isHealthcheck(path) && key == "test" {
		return cloneSourced(override)
	}
	return mergeSourced(base, override, child)
}

func isServiceAttribute(path []string) bool {
	return len(path) == 2 && path[0] == "services"
}

func isHealthcheck(path []string) bool {
	return len(path) == 3 && path[0] == "services" && path[2] == "healthcheck"
}

func isBuildAttribute(path []string) bool {
	return len(path) == 3 && path[0] == "services" && path[2] == "build"
}

func isDeployAttribute(path []string) bool {
	return len(path) == 3 && path[0] == "services" && path[2] == "deploy"
}

func isKeyValueAttribute(path []string, key string) bool {
	if isServiceAttribute(path) {
		switch key {
		case "environment", "labels", "annotations", "extra_hosts", "sysctls":
			return true
		}
	}
	if isBuildAttribute(path) {
		switch key {
		case "args", "labels", "extra_hosts":
			return true
		}
	}
	if isDeployAttribute(path) && key == "labels" {
		return true
	}
	return false
}

func asKeyValueMapping(node sourcedNode) sourcedNode {
	if node.Kind == yaml.MappingNode {
		return node
	}
	out := sourcedNode{Kind: yaml.MappingNode, Tag: "!!map", Source: node.Source, Pointer: node.Pointer}
	if node.Kind != yaml.SequenceNode {
		return out
	}
	for _, item := range node.Content {
		if item.Kind != yaml.ScalarNode {
			continue
		}
		name, value, found := strings.Cut(item.Value, "=")
		if !found {
			name, value, found = strings.Cut(item.Value, ":")
		}
		key := sourcedNode{Kind: yaml.ScalarNode, Tag: "!!str", Value: strings.TrimSpace(name), Source: item.Source, Pointer: item.Pointer}
		val := sourcedNode{Kind: yaml.ScalarNode, Tag: "!!null", Style: item.Style, Source: item.Source, Pointer: item.Pointer}
		if found {
			val.Tag = "!!str"
			val.Value = value
		}
		out.Content = append(out.Content, key, val)
	}
	return out
}

func appendSequences(base, override sourcedNode) sourcedNode {
	out := cloneSourced(base)
	for _, item := range override.Content {
		out.Content = append(out.Content, cloneSourced(item))
	}
	return out
}

func mergeUniqueSequence(base, override sourcedNode, keyOf func(sourcedNode) string, expand func(sourcedNode) sourcedNode) sourcedNode {
	if isOverrideTag(override) {
		return cloneSourced(stripComposeTag(override))
	}
	if base.Kind != yaml.SequenceNode || override.Kind != yaml.SequenceNode {
		return cloneSourced(override)
	}
	out := sourcedNode{Kind: yaml.SequenceNode, Tag: "!!seq", Source: base.Source, Pointer: base.Pointer}
	index := make(map[string]int)
	for _, item := range base.Content {
		item = cloneSourced(item)
		if key := keyOf(item); key != "" {
			index[key] = len(out.Content)
		}
		out.Content = append(out.Content, item)
	}
	for _, item := range override.Content {
		item = cloneSourced(item)
		key := keyOf(item)
		if key != "" {
			if pos, exists := index[key]; exists {
				out.Content[pos] = mergeUniqueResource(out.Content[pos], item, expand)
				continue
			}
			index[key] = len(out.Content)
		}
		out.Content = append(out.Content, item)
	}
	return out
}

func mergeUniqueResource(base, override sourcedNode, expand func(sourcedNode) sourcedNode) sourcedNode {
	if base.Kind != yaml.MappingNode && override.Kind != yaml.MappingNode {
		return cloneSourced(override)
	}
	return mergeSourced(expand(base), expand(override), nil)
}

func expandPort(item sourcedNode) sourcedNode {
	if item.Kind == yaml.MappingNode || item.Kind != yaml.ScalarNode {
		return item
	}
	ip, published, target, protocol := parsePortShort(item.Value)
	return longFormMapping(item, map[string]string{
		"host_ip":   ip,
		"published": published,
		"target":    target,
		"protocol":  protocol,
	})
}

func expandVolume(item sourcedNode) sourcedNode {
	if item.Kind == yaml.MappingNode || item.Kind != yaml.ScalarNode {
		return item
	}
	source, target, mode := parseVolumeShort(item.Value)
	return longFormMapping(item, map[string]string{
		"source": source,
		"target": target,
		"mode":   mode,
	})
}

func expandNamedResource(item sourcedNode) sourcedNode {
	if item.Kind == yaml.MappingNode || item.Kind != yaml.ScalarNode {
		return item
	}
	name := strings.TrimSpace(item.Value)
	return longFormMapping(item, map[string]string{"source": name, "target": name})
}

func longFormMapping(item sourcedNode, fields map[string]string) sourcedNode {
	out := sourcedNode{Kind: yaml.MappingNode, Tag: "!!map", Source: item.Source, Pointer: item.Pointer}
	for _, name := range []string{"host_ip", "published", "protocol", "source", "target", "mode"} {
		value := fields[name]
		if value == "" {
			continue
		}
		out.Content = append(out.Content,
			sourcedNode{Kind: yaml.ScalarNode, Tag: "!!str", Value: name, Source: item.Source, Pointer: item.Pointer},
			sourcedNode{Kind: yaml.ScalarNode, Tag: "!!str", Value: value, Style: item.Style, Source: item.Source, Pointer: item.Pointer},
		)
	}
	return out
}

func portUniqueKey(item sourcedNode) string {
	if item.Kind == yaml.MappingNode {
		return strings.Join([]string{
			mappingScalar(item, "host_ip"),
			mappingScalar(item, "target"),
			mappingScalar(item, "published"),
			mappingScalar(item, "protocol"),
		}, "\x00")
	}
	if item.Kind != yaml.ScalarNode {
		return ""
	}
	ip, published, target, protocol := parsePortShort(item.Value)
	return ip + "\x00" + target + "\x00" + published + "\x00" + protocol
}

func parsePortShort(s string) (ip, published, target, protocol string) {
	s = strings.TrimSpace(s)
	if slash := strings.LastIndex(s, "/"); slash >= 0 && !strings.Contains(s[slash+1:], ":") {
		protocol = s[slash+1:]
		s = s[:slash]
	}
	if strings.HasPrefix(s, "[") {
		if end := strings.Index(s, "]"); end >= 0 {
			ip = s[1:end]
			s = strings.TrimPrefix(s[end+1:], ":")
		}
	}
	parts := strings.Split(s, ":")
	switch len(parts) {
	case 1:
		target = parts[0]
	case 2:
		published, target = parts[0], parts[1]
	default:
		if len(parts) >= 3 {
			ip = parts[0]
			published = parts[1]
			target = strings.Join(parts[2:], ":")
		}
	}
	return ip, published, target, protocol
}

func resourceTargetKey(item sourcedNode) string {
	if item.Kind == yaml.MappingNode {
		if target := mappingScalar(item, "target"); target != "" {
			return target
		}
		return mappingScalar(item, "source")
	}
	if item.Kind != yaml.ScalarNode {
		return ""
	}
	_, target, _ := parseVolumeShort(item.Value)
	return target
}

func parseVolumeShort(s string) (source, target, mode string) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	switch {
	case len(parts) == 1:
		return "", parts[0], ""
	case len(parts) >= 3 && isVolumeMode(parts[len(parts)-1]):
		return strings.Join(parts[:len(parts)-2], ":"), parts[len(parts)-2], parts[len(parts)-1]
	default:
		return strings.Join(parts[:len(parts)-1], ":"), parts[len(parts)-1], ""
	}
}

func isVolumeMode(s string) bool {
	if s == "" {
		return false
	}
	for _, part := range strings.Split(s, ",") {
		switch part {
		case "rw", "ro", "z", "Z", "rprivate", "rshared", "rslave", "private", "shared", "slave", "cached", "delegated", "consistent":
		default:
			return false
		}
	}
	return true
}

func mappingValue(node sourcedNode, key string) sourcedNode {
	switch node.Kind {
	case yaml.SequenceNode:
		for _, item := range node.Content {
			if got := mappingValue(item, key); !isZeroSourced(got) {
				return got
			}
		}
	case yaml.MappingNode:
		var merges []sourcedNode
		for i := 0; i+1 < len(node.Content); i += 2 {
			name := strings.TrimSpace(node.Content[i].Value)
			if name == key {
				return node.Content[i+1]
			}
			if name == "<<" {
				merges = append(merges, node.Content[i+1])
			}
		}
		for _, merge := range merges {
			if got := mappingValue(merge, key); !isZeroSourced(got) {
				return got
			}
		}
	}
	return sourcedNode{}
}

func mappingPairs(node sourcedNode) []mappingPair {
	node = flattenMapping(node)
	if node.Kind != yaml.MappingNode {
		return nil
	}
	var pairs []mappingPair
	for i := 0; i+1 < len(node.Content); i += 2 {
		pairs = append(pairs, mappingPair{key: node.Content[i], value: node.Content[i+1]})
	}
	return pairs
}

func resolveMergeKeys(node sourcedNode) sourcedNode {
	switch node.Kind {
	case yaml.DocumentNode:
		for i := range node.Content {
			node.Content[i] = resolveMergeKeys(node.Content[i])
		}
		return node
	case yaml.MappingNode:
		node = flattenMapping(node)
		for i := 1; i < len(node.Content); i += 2 {
			node.Content[i] = resolveMergeKeys(node.Content[i])
		}
		return node
	case yaml.SequenceNode:
		for i := range node.Content {
			node.Content[i] = resolveMergeKeys(node.Content[i])
		}
		return node
	default:
		return node
	}
}

func flattenMapping(node sourcedNode) sourcedNode {
	if node.Kind != yaml.MappingNode {
		return node
	}
	type pair struct {
		key   sourcedNode
		value sourcedNode
	}
	var out []pair
	index := make(map[string]int)
	add := func(key, value sourcedNode, overwrite bool) {
		name := strings.TrimSpace(key.Value)
		if name == "" || name == "<<" {
			return
		}
		if pos, exists := index[name]; exists {
			if overwrite {
				out[pos].value = value
			}
			return
		}
		index[name] = len(out)
		out = append(out, pair{key: key, value: value})
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i]
		value := node.Content[i+1]
		if strings.TrimSpace(key.Value) == "<<" {
			for _, source := range mergeKeySources(value) {
				flat := flattenMapping(source)
				for j := 0; j+1 < len(flat.Content); j += 2 {
					add(flat.Content[j], flat.Content[j+1], false)
				}
			}
			continue
		}
		add(key, value, true)
	}
	result := sourcedNode{Kind: yaml.MappingNode, Tag: "!!map", Source: node.Source, Pointer: node.Pointer}
	for _, item := range out {
		result.Content = append(result.Content, item.key, item.value)
	}
	return result
}

func mergeKeySources(node sourcedNode) []sourcedNode {
	switch node.Kind {
	case yaml.MappingNode:
		return []sourcedNode{node}
	case yaml.SequenceNode:
		var out []sourcedNode
		for _, item := range node.Content {
			if item.Kind == yaml.MappingNode {
				out = append(out, item)
			}
		}
		return out
	default:
		return nil
	}
}

func mappingScalar(node sourcedNode, key string) string {
	return sourcedScalar(mappingValue(node, key))
}

func unwrapDocument(node sourcedNode) sourcedNode {
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		return node.Content[0]
	}
	return node
}

func cloneSourced(node sourcedNode) sourcedNode {
	out := node
	if len(node.Content) == 0 {
		out.Content = nil
		return out
	}
	out.Content = make([]sourcedNode, len(node.Content))
	for i, child := range node.Content {
		out.Content[i] = cloneSourced(child)
	}
	return out
}

func isZeroSourced(node sourcedNode) bool {
	return node.Kind == 0 && node.Tag == "" && node.Value == "" && len(node.Content) == 0
}

func isReset(node sourcedNode) bool {
	return composeTag(node) == "reset"
}

func isOverrideTag(node sourcedNode) bool {
	return composeTag(node) == "override"
}

func composeTag(node sourcedNode) string {
	tag := strings.TrimPrefix(node.Tag, "!")
	if tag == "reset" || tag == "override" {
		return tag
	}
	return ""
}

func stripComposeTag(node sourcedNode) sourcedNode {
	node.Tag = ""
	return node
}
