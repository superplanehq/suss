package php

import (
	"encoding/json"
	"strings"
)

type composerManifest struct {
	Require    map[string]string          `json:"require"`
	RequireDev map[string]string          `json:"require-dev"`
	Scripts    map[string]json.RawMessage `json:"scripts"`
	Config     composerConfig             `json:"config"`
}

type composerConfig struct {
	Platform map[string]json.RawMessage `json:"platform"`
}

func hasPackage(manifest composerManifest, name string) bool {
	_, ok := packagePointer(manifest, name)
	return ok
}

func packagePointer(manifest composerManifest, name string) (string, bool) {
	if _, ok := manifest.Require[name]; ok {
		return "/require/" + pointerToken(name), true
	}
	if _, ok := manifest.RequireDev[name]; ok {
		return "/require-dev/" + pointerToken(name), true
	}
	return "", false
}

func requirePHP(manifest composerManifest) string {
	return strings.TrimSpace(manifest.Require["php"])
}

func platformPHP(manifest composerManifest) string {
	raw, ok := manifest.Config.Platform["php"]
	if !ok {
		return ""
	}
	var version string
	if err := json.Unmarshal(raw, &version); err != nil {
		return ""
	}
	return strings.TrimSpace(version)
}

func scriptBodies(raw json.RawMessage) []string {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if text = strings.TrimSpace(text); text != "" {
			return []string{text}
		}
		return nil
	}

	var parts []string
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil
	}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func expandComposerAt(body string) string {
	body = strings.TrimSpace(body)
	if after, ok := strings.CutPrefix(body, "@php"); ok {
		after = strings.TrimSpace(after)
		if after == "" {
			return "php"
		}
		return "php " + after
	}
	return body
}

func scriptInvocations(raw json.RawMessage) string {
	bodies := scriptBodies(raw)
	commands := make([]string, 0, len(bodies))
	for _, body := range bodies {
		commands = append(commands, expandComposerAt(body))
	}
	return strings.Join(commands, " && ")
}

var composerEventScripts = map[string]struct{}{
	"pre-install-cmd": {}, "post-install-cmd": {},
	"pre-update-cmd": {}, "post-update-cmd": {},
	"pre-status-cmd": {}, "post-status-cmd": {},
	"pre-archive-cmd": {}, "post-archive-cmd": {},
	"pre-autoload-dump": {}, "post-autoload-dump": {},
	"post-root-package-install": {},
	"post-create-project-cmd":   {},
	"pre-uninstall-cmd":         {}, "post-uninstall-cmd": {},
	"pre-package-install": {}, "post-package-install": {},
	"pre-package-update": {}, "post-package-update": {},
	"pre-package-uninstall": {}, "post-package-uninstall": {},
	"init": {}, "command": {},
	"pre-file-download": {}, "post-file-download": {},
	"pre-file-dump": {}, "post-file-dump": {},
	"pre-operations-exec": {},
	"pre-pool-create":     {},
	"pre-command-run":     {}, "post-command-run": {},
}

func isComposerEventScript(name string) bool {
	_, ok := composerEventScripts[name]
	return ok
}
