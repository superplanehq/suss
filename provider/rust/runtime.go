package rust

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

type runtimePin struct {
	version  string
	evidence plan.Evidence
}

func runtimeFindings(ctx provider.Context, manifest cargoManifest) ([]plan.Finding, error) {
	var pins []runtimePin

	toolchain, err := readToolchainPin(ctx)
	if err != nil {
		return nil, err
	}
	if toolchain != nil {
		pins = append(pins, *toolchain)
	}

	rustVersionFile, err := readAncestorRustVersion(ctx)
	if err != nil {
		return nil, err
	}
	if rustVersionFile != nil {
		pins = append(pins, *rustVersionFile)
	}

	toolVersion, err := readAncestorToolVersion(ctx)
	if err != nil {
		return nil, err
	}
	if toolVersion != nil {
		pins = append(pins, *toolVersion)
	}

	if pin, err := cargoRustVersionPin(ctx, manifest); err != nil {
		return nil, err
	} else if pin != nil {
		pins = append(pins, *pin)
	}

	return mergeRuntimePins(ctx, pins), nil
}

func mergeRuntimePins(ctx provider.Context, pins []runtimePin) []plan.Finding {
	if len(pins) == 0 {
		return nil
	}

	grouped := make(map[string][]plan.Evidence)
	order := make([]string, 0, len(pins))
	for _, pin := range pins {
		if pin.version == "" {
			continue
		}
		if _, ok := grouped[pin.version]; !ok {
			order = append(order, pin.version)
		}
		grouped[pin.version] = append(grouped[pin.version], pin.evidence)
	}

	findings := make([]plan.Finding, 0, len(order))
	for _, version := range order {
		findings = append(findings, runtimeFinding(ctx, version, grouped[version]))
	}
	return findings
}

func cargoRustVersionPin(ctx provider.Context, manifest cargoManifest) (*runtimePin, error) {
	if manifest.RustVersion != "" {
		return &runtimePin{
			version: manifest.RustVersion,
			evidence: plan.Evidence{
				Kind:    plan.EvidenceDeclaration,
				Source:  ctx.SourcePath("Cargo.toml"),
				Pointer: "/package/rust-version",
			},
		}, nil
	}
	if manifest.WorkspaceRustVersion != "" {
		return &runtimePin{
			version: manifest.WorkspaceRustVersion,
			evidence: plan.Evidence{
				Kind:    plan.EvidenceDeclaration,
				Source:  ctx.SourcePath("Cargo.toml"),
				Pointer: "/workspace/package/rust-version",
			},
		}, nil
	}
	if !manifest.RustVersionWorkspace {
		return nil, nil
	}

	version, source, err := ancestorWorkspaceRustVersion(ctx)
	if err != nil || version == "" {
		return nil, err
	}
	return &runtimePin{
		version: version,
		evidence: plan.Evidence{
			Kind:        plan.EvidenceDeclaration,
			Source:      source,
			Pointer:     "/workspace/package/rust-version",
			Description: "Cargo.toml inherits rust-version from the workspace.",
		},
	}, nil
}

func ancestorWorkspaceRustVersion(ctx provider.Context) (string, string, error) {
	dir := filepath.Dir(ctx.ProjectDir())
	for {
		path := filepath.Join(dir, "Cargo.toml")
		contents, err := os.ReadFile(path)
		switch {
		case err == nil:
			parsed := parseCargoTOML(string(contents))
			if parsed.WorkspaceRustVersion != "" {
				relative, relErr := filepath.Rel(ctx.RepositoryRoot, path)
				if relErr != nil {
					return "", "", fmt.Errorf("resolve workspace Cargo.toml source: %w", relErr)
				}
				return parsed.WorkspaceRustVersion, filepath.ToSlash(relative), nil
			}
		case !os.IsNotExist(err):
			return "", "", fmt.Errorf("read %s: %w", path, err)
		}
		if dir == ctx.RepositoryRoot || !strings.HasPrefix(dir, ctx.RepositoryRoot+string(filepath.Separator)) {
			return "", "", nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", nil
		}
		dir = parent
	}
}

func readToolchainPin(ctx provider.Context) (*runtimePin, error) {
	for _, name := range []string{"rust-toolchain.toml", "rust-toolchain"} {
		path, contents, ok, err := readProjectOrAncestor(ctx, name)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		channel := parseToolchainFile(contents).Channel
		if channel == "" {
			continue
		}
		return &runtimePin{
			version:  channel,
			evidence: plan.Evidence{Kind: plan.EvidenceDeclaration, Source: path},
		}, nil
	}
	return nil, nil
}

func readAncestorRustVersion(ctx provider.Context) (*runtimePin, error) {
	path, contents, ok, err := readProjectOrAncestor(ctx, "rust-version")
	if err != nil || !ok {
		return nil, err
	}
	version := firstVersionLine(contents)
	if version == "" {
		return nil, nil
	}
	return &runtimePin{
		version:  version,
		evidence: plan.Evidence{Kind: plan.EvidenceDeclaration, Source: path},
	}, nil
}

func readAncestorToolVersion(ctx provider.Context) (*runtimePin, error) {
	path, contents, ok, err := readProjectOrAncestor(ctx, ".tool-versions")
	if err != nil || !ok {
		return nil, err
	}
	scanner := bufio.NewScanner(strings.NewReader(contents))
	for scanner.Scan() {
		line, _, _ := strings.Cut(scanner.Text(), "#")
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "rust" {
			return &runtimePin{
				version:  fields[1],
				evidence: plan.Evidence{Kind: plan.EvidenceDeclaration, Source: path, Pointer: "/rust"},
			}, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return nil, nil
}

func readProjectOrAncestor(ctx provider.Context, name string) (string, string, bool, error) {
	dir := ctx.ProjectDir()
	for {
		path := filepath.Join(dir, name)
		contents, err := os.ReadFile(path)
		switch {
		case err == nil:
			relative, relErr := filepath.Rel(ctx.RepositoryRoot, path)
			if relErr != nil {
				return "", "", false, fmt.Errorf("resolve %s source: %w", name, relErr)
			}
			return filepath.ToSlash(relative), string(contents), true, nil
		case !os.IsNotExist(err):
			return "", "", false, fmt.Errorf("read %s: %w", path, err)
		}
		if dir == ctx.RepositoryRoot || !strings.HasPrefix(dir, ctx.RepositoryRoot+string(filepath.Separator)) {
			return "", "", false, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", false, nil
		}
		dir = parent
	}
}

func runtimeFinding(ctx provider.Context, version string, evidence []plan.Evidence) plan.Finding {
	return plan.RequirementFinding{
		ProjectPath: ctx.ProjectPath,
		Detector:    providerName,
		Requirement: plan.Requirement{
			Kind:       plan.RequirementRuntime,
			Name:       "rust",
			Version:    version,
			Confidence: plan.ConfidenceHigh,
			Evidence:   evidence,
		},
	}
}
