package php

import (
	"encoding/json"
	"path"
	"strings"

	"github.com/superplanehq/suss/plan"
)

type composerManifest struct {
	Require    map[string]string          `json:"require"`
	RequireDev map[string]string          `json:"require-dev"`
	Scripts    map[string]json.RawMessage `json:"scripts"`
	Config     composerConfig             `json:"config"`
}

type composerConfig struct {
	Platform  map[string]json.RawMessage `json:"platform"`
	BinDir    json.RawMessage            `json:"bin-dir"`
	VendorDir json.RawMessage            `json:"vendor-dir"`
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

func composerBinary(manifest composerManifest, name string) string {
	dir, ok := resolveComposerBinDir(manifest)
	if !ok {
		return "composer exec " + name
	}
	return path.Join(dir, name)
}

func resolveComposerBinDir(manifest composerManifest) (string, bool) {
	vendorDir := configString(manifest.Config.VendorDir)
	if vendorDir == "" {
		vendorDir = "vendor"
	}
	if !usableComposerDir(vendorDir) {
		return "", false
	}
	binDir := configString(manifest.Config.BinDir)
	if binDir == "" {
		return path.Clean(path.Join(vendorDir, "bin")), true
	}
	binDir = strings.ReplaceAll(binDir, "{$vendor-dir}", vendorDir)
	if !usableComposerDir(binDir) {
		return "", false
	}
	return path.Clean(binDir), true
}

func composerBinEvidence(source string, manifest composerManifest) []plan.Evidence {
	if configString(manifest.Config.BinDir) != "" {
		return []plan.Evidence{{
			Kind:    plan.EvidenceDeclaration,
			Source:  source,
			Pointer: "/config/bin-dir",
		}}
	}
	if configString(manifest.Config.VendorDir) != "" {
		return []plan.Evidence{{
			Kind:    plan.EvidenceDeclaration,
			Source:  source,
			Pointer: "/config/vendor-dir",
		}}
	}
	return nil
}

func configString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func usableComposerDir(value string) bool {
	return value != "" && !strings.Contains(value, "{$") && !strings.Contains(value, "${")
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
