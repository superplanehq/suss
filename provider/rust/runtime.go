package rust

import (
	"bufio"
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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
		confidence := plan.ConfidenceHigh
		if incompatible, _ := pinsIncompatibleWithMSRV(exact, msrv); incompatible {
			confidence = plan.ConfidenceMedium
		}
		findings = append(findings, runtimeFinding(ctx, exact[0].version, confidence, evidence))
	}

	grouped, order := groupPinEvidence(msrv)
	for _, version := range order {
		findings = append(findings, runtimeFinding(ctx, version, plan.ConfidenceHigh, grouped[version]))
	}
	conflicts = append(conflicts, msrvConflicts(exact, msrv)...)
	return findings, conflicts
}

func pinsIncompatibleWithMSRV(exact, msrv []runtimePin) (incompatible, known bool) {
	for _, pin := range exact {
		for _, constraint := range msrv {
			ok, pinKnown := exactSatisfiesMSRV(pin.version, constraint.version)
			if !pinKnown {
				continue
			}
			known = true
			if !ok {
				return true, true
			}
		}
	}
	return false, known
}

func msrvConflicts(exact, msrv []runtimePin) []plan.Conflict {
	groupedExact, exactOrder := groupPinEvidence(exact)
	groupedMSRV, msrvOrder := groupPinEvidence(msrv)
	var conflicts []plan.Conflict
	for _, pinVersion := range exactOrder {
		for _, constraint := range msrvOrder {
			ok, known := exactSatisfiesMSRV(pinVersion, constraint)
			if !known || ok {
				continue
			}
			conflicts = append(conflicts, plan.Conflict{
				Subject: "runtime.rust.version",
				Message: "The pinned Rust toolchain does not satisfy Cargo rust-version.",
				Assertions: []plan.Candidate{
					{Value: pinVersion, Evidence: groupedExact[pinVersion]},
					{Value: constraint, Evidence: groupedMSRV[constraint]},
				},
			})
		}
	}
	return conflicts
}

func exactSatisfiesMSRV(exact, constraint string) (ok, known bool) {
	exact = strings.TrimPrefix(strings.TrimSpace(exact), "v")
	constraint = strings.TrimSpace(constraint)
	if !comparableRustVersion(exact) {
		return false, false
	}

	bound := constraint
	minExclusive := false
	switch {
	case strings.HasPrefix(constraint, ">="):
		bound = strings.TrimSpace(constraint[2:])
	case strings.HasPrefix(constraint, ">"):
		bound = strings.TrimSpace(constraint[1:])
		minExclusive = true
	case strings.ContainsAny(constraint, "<^~*|"):
		return false, false
	}
	bound = strings.TrimPrefix(bound, "v")
	if !comparableRustVersion(bound) {
		return false, false
	}
	cmp := compareRustVersions(exact, bound)
	if minExclusive {
		return cmp > 0, true
	}
	return cmp >= 0, true
}

func comparableRustVersion(version string) bool {
	if version == "" {
		return false
	}
	for _, part := range strings.Split(version, ".") {
		if _, err := strconv.Atoi(part); err != nil {
			return false
		}
	}
	return true
}

func compareRustVersions(a, b string) int {
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	n := max(len(as), len(bs))
	for i := 0; i < n; i++ {
		if n := cmp.Compare(rustVersionPart(as, i), rustVersionPart(bs, i)); n != 0 {
			return n
		}
	}
	return 0
}

func rustVersionPart(parts []string, i int) int {
	if i >= len(parts) {
		return 0
	}
	n, err := strconv.Atoi(parts[i])
	if err != nil {
		return 0
	}
	return n
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
			version: cargoMinimumVersion(manifest.RustVersion),
			evidence: plan.Evidence{
				Kind:    plan.EvidenceDeclaration,
				Source:  ctx.SourcePath("Cargo.toml"),
				Pointer: "/package/rust-version",
			},
		}, nil
	}
	if manifest.WorkspaceRustVersion != "" {
		return &runtimePin{
			version: cargoMinimumVersion(manifest.WorkspaceRustVersion),
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
		version: cargoMinimumVersion(version),
		evidence: plan.Evidence{
			Kind:        plan.EvidenceDeclaration,
			Source:      source,
			Pointer:     "/workspace/package/rust-version",
			Description: "Cargo.toml inherits rust-version from the workspace.",
		},
	}, nil
}

func cargoMinimumVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" {
		return ""
	}
	switch {
	case strings.HasPrefix(version, ">="), strings.HasPrefix(version, ">"), strings.HasPrefix(version, "<"), strings.HasPrefix(version, "^"), strings.HasPrefix(version, "~"):
		return version
	default:
		return ">=" + version
	}
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
	channel := parseToolchainFile(path, contents).Channel
	if channel == "" {
		return nil, nil
	}
	return &runtimePin{
		version:  channel,
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
