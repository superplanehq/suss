// Package rust detects Cargo packages from Cargo.toml and related files.
package rust

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

const providerName = "rust"

// Provider detects Rust projects from Cargo.toml and related files.
type Provider struct{}

// Name returns the stable provider identifier.
func (Provider) Name() string {
	return providerName
}

// Detect inspects one project root. It returns an empty result when the
// directory has no Cargo.toml.
func (Provider) Detect(ctx provider.Context) (provider.Result, error) {
	manifest, ok, err := readCargoTOML(ctx)
	if err != nil {
		return provider.Result{}, err
	}
	if !ok {
		return provider.Result{}, nil
	}

	var result provider.Result
	result.Findings = append(result.Findings, projectFindings(ctx, manifest)...)
	runtimes, conflicts, err := runtimeFindings(ctx, manifest)
	if err != nil {
		return provider.Result{}, err
	}
	result.Findings = append(result.Findings, runtimes...)
	result.Conflicts = append(result.Conflicts, conflicts...)
	result.Findings = append(result.Findings, toolFindings(ctx)...)

	commands, err := inferredCommands(ctx)
	if err != nil {
		return provider.Result{}, err
	}
	result.Findings = append(result.Findings, commands...)
	return result, nil
}

func readCargoTOML(ctx provider.Context) (cargoManifest, bool, error) {
	path := filepath.Join(ctx.ProjectDir(), "Cargo.toml")
	contents, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cargoManifest{}, false, nil
	}
	if err != nil {
		return cargoManifest{}, false, fmt.Errorf("read Cargo.toml: %w", err)
	}
	return parseCargoTOML(string(contents)), true, nil
}

func projectFindings(ctx provider.Context, manifest cargoManifest) []plan.Finding {
	source := ctx.SourcePath("Cargo.toml")
	language := plan.Evidence{Kind: plan.EvidenceDeclaration, Source: source}
	switch {
	case manifest.Name != "":
		language.Pointer = "/package/name"
	case manifest.HasPackage:
		language.Pointer = "/package"
	case manifest.HasWorkspace:
		language.Pointer = "/workspace"
	}

	findings := []plan.Finding{
		propertyFinding(ctx, plan.PropertyLanguage, "rust", "", []plan.Evidence{language}),
		propertyFinding(ctx, plan.PropertyPackageManager, "cargo", "", cargoManagerEvidence(ctx)),
	}
	if manifest.HasWorkspace {
		findings = append(findings, propertyFinding(ctx, plan.PropertyFact, "workspace.orchestrator", "cargo", []plan.Evidence{{
			Kind:    plan.EvidenceConfiguration,
			Source:  source,
			Pointer: "/workspace",
		}}))
	}
	for _, framework := range packageFrameworks(manifest.Dependencies) {
		pointer := framework.Key
		if pointer == "" {
			pointer = framework.Name
		}
		findings = append(findings, propertyFinding(ctx, plan.PropertyFramework, framework.Name, "", []plan.Evidence{{
			Kind:        plan.EvidenceDeclaration,
			Source:      source,
			Pointer:     "/dependencies/" + pointerToken(pointer),
			Description: "The Cargo dependency list includes " + framework.Name + ".",
		}}))
	}
	return findings
}

func cargoManagerEvidence(ctx provider.Context) []plan.Evidence {
	evidence := []plan.Evidence{{
		Kind:   plan.EvidenceDeclaration,
		Source: ctx.SourcePath("Cargo.toml"),
	}}
	if fileExists(ctx.ProjectDir(), "Cargo.lock") {
		evidence = append(evidence, plan.Evidence{
			Kind:   plan.EvidenceFile,
			Source: ctx.SourcePath("Cargo.lock"),
		})
	}
	return evidence
}

func propertyFinding(ctx provider.Context, kind plan.PropertyKind, name, value string, evidence []plan.Evidence) plan.Finding {
	return plan.PropertyFinding{
		ProjectPath: ctx.ProjectPath,
		Detector:    providerName,
		Property: plan.Property{
			Kind:       kind,
			Name:       name,
			Value:      value,
			Confidence: plan.ConfidenceHigh,
			Evidence:   evidence,
		},
	}
}

func fileExists(dir, name string) bool {
	info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name)))
	return err == nil && !info.IsDir()
}

func stringPtr(value string) *string {
	return &value
}

func pointerToken(value string) string {
	value = strings.ReplaceAll(value, "~", "~0")
	return strings.ReplaceAll(value, "/", "~1")
}

func walkSkipDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "vendor", "testdata", "node_modules", "_build", "deps", "dist", "target", "tmp":
		return true
	default:
		return false
	}
}

func findTestFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path == root {
				return nil
			}
			if walkSkipDir(entry.Name()) || entry.Type()&os.ModeSymlink != 0 {
				return filepath.SkipDir
			}
			if fileExists(path, "Cargo.toml") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".rs") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		ok, err := isRustTestFile(path, rel)
		if err != nil {
			return err
		}
		if ok {
			files = append(files, rel)
		}
		return nil
	})
	return files, err
}

func isRustTestFile(abs, rel string) (bool, error) {
	if strings.HasPrefix(rel, "tests/") || strings.Contains(rel, "/tests/") {
		return true, nil
	}
	contents, err := os.ReadFile(abs)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", rel, err)
	}
	return rustSourceHasTest(string(contents)), nil
}

var rustTestAttribute = regexp.MustCompile(`#\[\s*(?:(?:[A-Za-z_][A-Za-z0-9_]*::)*test|rstest)(?:\s*(?:\(|]))`)

func rustSourceHasTest(contents string) bool {
	return rustTestAttribute.MatchString(stripRustCommentsAndStrings(contents))
}

func stripRustCommentsAndStrings(src string) string {
	b := []byte(src)
	out := make([]byte, 0, len(b))
	i := 0
	for i < len(b) {
		if i+1 < len(b) && b[i] == '/' && b[i+1] == '/' {
			for i < len(b) && b[i] != '\n' {
				i++
			}
			continue
		}
		if i+1 < len(b) && b[i] == '/' && b[i+1] == '*' {
			i = skipRustBlockComment(b, i+2)
			continue
		}
		if start, hashes, ok := rustRawStringStart(b, i); ok {
			i = skipRustRawString(b, start, hashes)
			continue
		}
		if end, ok := rustCharLiteralEnd(b, i); ok {
			i = end
			continue
		}
		if start, ok := rustCookedStringStart(b, i); ok {
			i = skipRustCookedString(b, start)
			continue
		}
		out = append(out, b[i])
		i++
	}
	return string(out)
}

func rustRawStringStart(b []byte, i int) (content, hashes int, ok bool) {
	if !rustTokenStart(b, i) {
		return 0, 0, false
	}
	j := i
	if j < len(b) && (b[j] == 'b' || b[j] == 'B' || b[j] == 'c' || b[j] == 'C') {
		j++
	}
	if j >= len(b) || (b[j] != 'r' && b[j] != 'R') {
		return 0, 0, false
	}
	j++
	for j < len(b) && b[j] == '#' {
		hashes++
		j++
	}
	if j >= len(b) || b[j] != '"' {
		return 0, 0, false
	}
	return j + 1, hashes, true
}

func skipRustRawString(b []byte, i, hashes int) int {
	for i < len(b) {
		if b[i] == '"' {
			n := 0
			for i+1+n < len(b) && b[i+1+n] == '#' {
				n++
			}
			if n == hashes {
				return i + 1 + n
			}
		}
		i++
	}
	return len(b)
}

func rustCharLiteralEnd(b []byte, i int) (int, bool) {
	if !rustTokenStart(b, i) {
		return 0, false
	}
	j := i
	if j < len(b) && (b[j] == 'b' || b[j] == 'B') {
		j++
	}
	if j >= len(b) || b[j] != '\'' {
		return 0, false
	}
	j++
	if j >= len(b) {
		return 0, false
	}
	switch b[j] {
	case '\\':
		j = skipRustCharEscape(b, j)
	case '\'':
		return 0, false
	default:
		_, size := utf8.DecodeRune(b[j:])
		j += size
	}
	if j < len(b) && b[j] == '\'' {
		return j + 1, true
	}
	return 0, false
}

func skipRustCharEscape(b []byte, i int) int {
	i++
	if i >= len(b) {
		return i
	}
	switch b[i] {
	case 'u', 'U':
		i++
		if i < len(b) && b[i] == '{' {
			i++
			for i < len(b) && b[i] != '}' {
				i++
			}
			if i < len(b) {
				i++
			}
		}
		return i
	case 'x', 'X':
		i++
		n := 0
		for n < 2 && i < len(b) && isRustHex(b[i]) {
			i++
			n++
		}
		return i
	default:
		return i + 1
	}
}

func isRustHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func rustCookedStringStart(b []byte, i int) (content int, ok bool) {
	if !rustTokenStart(b, i) {
		return 0, false
	}
	j := i
	if j < len(b) && (b[j] == 'b' || b[j] == 'B' || b[j] == 'c' || b[j] == 'C') {
		j++
	}
	if j >= len(b) || b[j] != '"' {
		return 0, false
	}
	return j + 1, true
}

func skipRustCookedString(b []byte, i int) int {
	for i < len(b) {
		if b[i] == '\\' && i+1 < len(b) {
			i += 2
			continue
		}
		if b[i] == '"' {
			return i + 1
		}
		i++
	}
	return len(b)
}

func skipRustBlockComment(b []byte, i int) int {
	depth := 1
	for i < len(b) && depth > 0 {
		if i+1 < len(b) && b[i] == '/' && b[i+1] == '*' {
			depth++
			i += 2
			continue
		}
		if i+1 < len(b) && b[i] == '*' && b[i+1] == '/' {
			depth--
			i += 2
			continue
		}
		i++
	}
	return i
}

func rustTokenStart(b []byte, i int) bool {
	if i == 0 {
		return true
	}
	c := b[i-1]
	return (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_'
}
