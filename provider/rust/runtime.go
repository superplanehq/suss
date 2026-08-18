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
	exact    bool
}

func runtimeFindings(ctx provider.Context, manifest cargoManifest) ([]plan.Finding, []plan.Conflict, error) {
	var pins []runtimePin

	toolchain, err := readToolchainPin(ctx)
	if err != nil {
		return nil, nil, err
	}
	if toolchain != nil {
		pins = append(pins, *toolchain)
	}

	rustVersionFile, err := readAncestorRustVersion(ctx)
	if err != nil {
		return nil, nil, err
	}
	if rustVersionFile != nil {
		pins = append(pins, *rustVersionFile)
	}

	toolVersion, err := readAncestorToolVersion(ctx)
	if err != nil {
		return nil, nil, err
	}
	if toolVersion != nil {
		pins = append(pins, *toolVersion)
	}

	if pin, err := cargoRustVersionPin(ctx, manifest); err != nil {
		return nil, nil, err
	} else if pin != nil {
		pins = append(pins, *pin)
	}

	findings, conflicts := mergeRuntimePins(ctx, pins)
	return findings, conflicts, nil
}

func mergeRuntimePins(ctx provider.Context, pins []runtimePin) ([]plan.Finding, []plan.Conflict) {
	var exact, msrv []runtimePin
	for _, pin := range pins {
		if pin.version == "" {
			continue
		}
		if pin.exact {
			exact = append(exact, pin)
		} else {
			msrv = append(msrv, pin)
		}
	}
	if len(exact) == 0 && len(msrv) == 0 {
		return nil, nil
	}

	var findings []plan.Finding
	var conflicts []plan.Conflict
	if versionsDisagree(exact) {
		grouped, order := groupPinEvidence(exact)
		assertions := make([]plan.Candidate, 0, len(order))
		for _, version := range order {
			evidence := grouped[version]
			findings = append(findings, runtimeFinding(ctx, version, plan.ConfidenceMedium, evidence))
			assertions = append(assertions, plan.Candidate{Value: version, Evidence: evidence})
		}
		conflicts = append(conflicts, plan.Conflict{
			Subject:    "runtime.rust.version",
			Message:    "Exact Rust version pins disagree.",
			Assertions: assertions,
		})
	} else if len(exact) > 0 {
		evidence := make([]plan.Evidence, 0, len(exact))
		for _, pin := range exact {
			evidence = append(evidence, pin.evidence)
		}
		findings = append(findings, runtimeFinding(ctx, exact[0].version, plan.ConfidenceHigh, evidence))
	}

	grouped, order := groupPinEvidence(msrv)
	for _, version := range order {
		findings = append(findings, runtimeFinding(ctx, version, plan.ConfidenceHigh, grouped[version]))
	}
	return findings, conflicts
}

func groupPinEvidence(pins []runtimePin) (map[string][]plan.Evidence, []string) {
	grouped := make(map[string][]plan.Evidence)
	order := make([]string, 0, len(pins))
	for _, pin := range pins {
		if _, ok := grouped[pin.version]; !ok {
			order = append(order, pin.version)
		}
		grouped[pin.version] = append(grouped[pin.version], pin.evidence)
	}
	return grouped, order
}

func versionsDisagree(pins []runtimePin) bool {
	for i := 1; i < len(pins); i++ {
		if pins[i].version != pins[0].version {
			return true
		}
	}
	return false
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
	path, contents, ok, err := readNearestNamedFile(ctx, "rust-toolchain.toml", "rust-toolchain")
	if err != nil || !ok {
		return nil, err
	}
	channel := parseToolchainFile(contents).Channel
	if channel == "" {
		return nil, nil
	}
	return &runtimePin{
		version:  channel,
		exact:    true,
		evidence: plan.Evidence{Kind: plan.EvidenceDeclaration, Source: path},
	}, nil
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
		exact:    true,
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
				exact:    true,
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
	return readNearestNamedFile(ctx, name)
}

func readNearestNamedFile(ctx provider.Context, names ...string) (string, string, bool, error) {
	dir := ctx.ProjectDir()
	for {
		for _, name := range names {
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

func runtimeFinding(ctx provider.Context, version string, confidence plan.Confidence, evidence []plan.Evidence) plan.Finding {
	return plan.RequirementFinding{
		ProjectPath: ctx.ProjectPath,
		Detector:    providerName,
		Requirement: plan.Requirement{
			Kind:       plan.RequirementRuntime,
			Name:       "rust",
			Version:    version,
			Confidence: confidence,
			Evidence:   evidence,
		},
	}
}
