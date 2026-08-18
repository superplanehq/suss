package gha

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/superplanehq/suss/knowledge"
	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

func extract(ctx provider.Context, source string, workflow workflowFile) (provider.Result, error) {
	var result provider.Result
	workflowDir := "."
	if workflow.Defaults != nil && workflow.Defaults.Run != nil {
		workflowDir = resolveDirectory(ctx.RepositoryRoot, ".", workflow.Defaults.Run.WorkingDirectory)
	}

	result.Findings = append(result.Findings, envFindings(source, "/env", ".", workflow.Env, true)...)

	for _, jobID := range sortedKeys(workflow.Jobs) {
		jobFindings, err := extractJob(ctx, source, workflowDir, jobID, workflow.Jobs[jobID])
		if err != nil {
			return provider.Result{}, err
		}
		result.Findings = append(result.Findings, jobFindings...)
	}
	return result, nil
}

func extractJob(ctx provider.Context, source, workflowDir, jobID string, job job) ([]plan.Finding, error) {
	jobPointer := jsonPointer("jobs", jobID)
	if job.Uses != "" {
		return []plan.Finding{limitationFinding(source, jobPointer+"/uses", "reusable-workflows", "Reusable workflows are detected but not expanded.")}, nil
	}

	var matrix map[string][]string
	if job.Strategy != nil {
		matrix = matrixValues(job.Strategy.Matrix)
	}

	jobWD := ""
	if job.Defaults != nil && job.Defaults.Run != nil {
		jobWD = job.Defaults.Run.WorkingDirectory
	}
	envDir := workflowDir
	if jobWD != "" && !isExpression(jobWD) {
		envDir = resolveDirectory(ctx.RepositoryRoot, workflowDir, jobWD)
	}

	var findings []plan.Finding
	findings = append(findings, envFindings(source, jobPointer+"/env", envDir, job.Env, false)...)
	findings = append(findings, serviceFindings(source, jobPointer, envDir, job.Services, matrix)...)

	for i, step := range job.Steps {
		stepPointer := jobPointer + jsonPointer("steps", strconv.Itoa(i))
		stepFindings, err := extractStep(ctx, source, workflowDir, jobWD, stepPointer, step, matrix)
		if err != nil {
			return nil, err
		}
		findings = append(findings, stepFindings...)
	}
	return findings, nil
}

func extractStep(ctx provider.Context, source, workflowDir, jobWD, stepPointer string, step step, matrix map[string][]string) ([]plan.Finding, error) {
	effectiveWD := jobWD
	if step.WorkingDirectory != "" {
		effectiveWD = step.WorkingDirectory
	}
	dirs := expandDirectories(ctx.RepositoryRoot, workflowDir, effectiveWD, matrix)

	var findings []plan.Finding
	for _, dir := range dirs {
		findings = append(findings, envFindings(source, stepPointer+"/env", dir, step.Env, false)...)
		if step.Uses != "" {
			findings = append(findings, usesFindings(ctx, source, dir, stepPointer, step, matrix)...)
			continue
		}
		if strings.TrimSpace(step.Run) == "" {
			continue
		}
		commands, err := runFindings(ctx, source, dir, stepPointer+"/run", step.Run, matrix)
		if err != nil {
			return nil, err
		}
		findings = append(findings, commands...)
	}
	return findings, nil
}

func expandDirectories(repo, base, rel string, matrix map[string][]string) []string {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return []string{base}
	}
	if axis, ok := matrixAxis(rel); ok {
		if values := matrix[axis]; len(values) > 0 {
			dirs := make([]string, 0, len(values))
			for _, value := range values {
				if isExpression(value) {
					continue
				}
				dirs = append(dirs, resolveDirectory(repo, base, value))
			}
			if len(dirs) > 0 {
				return dirs
			}
		}
		return []string{base}
	}
	if isExpression(rel) {
		return []string{base}
	}
	return []string{resolveDirectory(repo, base, rel)}
}

func isExpression(value string) bool {
	return strings.Contains(value, "${{")
}

func usesFindings(ctx provider.Context, source, dir, stepPointer string, step step, matrix map[string][]string) []plan.Finding {
	uses := actionName(step.Uses)
	if isLocalAction(step.Uses) {
		return []plan.Finding{limitationFinding(source, stepPointer+"/uses", "composite-actions", "Local composite actions are detected but not expanded.")}
	}
	switch uses {
	case "actions/setup-node":
		return setupRuntimeFindings(ctx, source, dir, stepPointer, "node", step.With, matrix, []string{"node-version", "node-version-file"})
	case "actions/setup-go":
		return setupRuntimeFindings(ctx, source, dir, stepPointer, "go", step.With, matrix, []string{"go-version", "go-version-file"})
	case "erlef/setup-beam":
		findings := setupRuntimeFindings(ctx, source, dir, stepPointer, "elixir", step.With, matrix, []string{"elixir-version"})
		return append(findings, setupRuntimeFindings(ctx, source, dir, stepPointer, "erlang", step.With, matrix, []string{"otp-version"})...)
	case "ruby/setup-ruby":
		if file := strings.TrimSpace(step.With["ruby-version"]); file != "" && !isExpression(file) {
			relative := resolveDirectory(ctx.RepositoryRoot, ".", file)
			if info, err := os.Stat(filepath.Join(ctx.RepositoryRoot, filepath.FromSlash(relative))); err == nil && !info.IsDir() {
				return versionFileFindings(ctx, source, dir, stepPointer, "ruby", "ruby-version", file)
			}
		}
		return setupRuntimeFindings(ctx, source, dir, stepPointer, "ruby", step.With, matrix, []string{"ruby-version"})
	case "shivammathur/setup-php":
		return setupRuntimeFindings(ctx, source, dir, stepPointer, "php", step.With, matrix, []string{"php-version"})
	case "actions/setup-java":
		return setupRuntimeFindings(ctx, source, dir, stepPointer, "java", step.With, matrix, []string{"java-version", "java-version-file"})
	case "golangci/golangci-lint-action":
		return golangciActionFindings(source, dir, stepPointer, step)
	default:
		return nil
	}
}

func golangciActionFindings(source, dir, stepPointer string, step step) []plan.Finding {
	run := "golangci-lint run"
	if args := strings.TrimSpace(step.With["args"]); args != "" {
		run += " " + args
	}
	stmt := knowledge.ParseStatementsKeepPipelines(run)
	if len(stmt) == 0 {
		return nil
	}
	command, err := observedCommand(source, dir, stepPointer+"/uses", stmt[0])
	if err != nil {
		return nil
	}
	return []plan.Finding{plan.CommandFinding{
		ProjectPath: dir,
		Detector:    providerName,
		Command:     command,
	}}
}

func setupRuntimeFindings(ctx provider.Context, source, dir, stepPointer, runtime string, with stringMap, matrix map[string][]string, keys []string) []plan.Finding {
	versionKey := keys[0]
	if len(keys) > 1 {
		fileKey := keys[1]
		if file := strings.TrimSpace(with[fileKey]); file != "" {
			return versionFileFindings(ctx, source, dir, stepPointer, runtime, fileKey, file)
		}
	}

	raw := strings.TrimSpace(with[versionKey])
	if raw == "" {
		// The version input may be omitted entirely; /uses always exists.
		return []plan.Finding{runtimeFinding(source, dir, stepPointer+"/uses", runtime, "", "The setup action does not pin a version.")}
	}

	versions := []string{raw}
	if axis, ok := matrixAxis(raw); ok {
		if values := matrix[axis]; len(values) > 0 {
			versions = values
		} else {
			return []plan.Finding{runtimeFinding(source, dir, stepPointer+"/with/"+versionKey, runtime, "", "The setup action takes its version from a matrix axis that was not enumerated.")}
		}
	} else if strings.Contains(raw, "${{") {
		return []plan.Finding{runtimeFinding(source, dir, stepPointer+"/with/"+versionKey, runtime, "", "The setup action takes its version from an expression that was not resolved.")}
	}

	findings := make([]plan.Finding, 0, len(versions))
	for _, version := range versions {
		description := ""
		if len(versions) > 1 {
			description = fmt.Sprintf("CI tests %s %s as part of a job matrix.", runtime, version)
		}
		findings = append(findings, runtimeFinding(source, dir, stepPointer+"/with/"+versionKey, runtime, version, description))
	}
	return findings
}

func versionFileFindings(ctx provider.Context, source, dir, stepPointer, runtime, fileKey, file string) []plan.Finding {
	pointer := stepPointer + "/with/" + fileKey
	evidence := []plan.Evidence{{
		Kind:        plan.EvidenceInvocation,
		Source:      source,
		Pointer:     pointer,
		Description: fmt.Sprintf("The setup action reads %s.", file),
	}}

	rel := resolveDirectory(ctx.RepositoryRoot, ".", file)
	abs := filepath.Join(ctx.RepositoryRoot, filepath.FromSlash(rel))
	contents, err := os.ReadFile(abs)
	if err == nil {
		if version := firstVersionLine(string(contents)); version != "" && !strings.Contains(version, " ") && !strings.Contains(version, "\t") {
			evidence = append(evidence, plan.Evidence{
				Kind:   plan.EvidenceDeclaration,
				Source: rel,
			})
			return []plan.Finding{requirementFinding(dir, plan.Requirement{
				Kind:       plan.RequirementRuntime,
				Name:       runtime,
				Version:    version,
				Confidence: plan.ConfidenceHigh,
				Evidence:   evidence,
			})}
		}
	}

	return []plan.Finding{requirementFinding(dir, plan.Requirement{
		Kind:       plan.RequirementRuntime,
		Name:       runtime,
		Confidence: plan.ConfidenceMedium,
		Evidence:   evidence,
	})}
}

func firstVersionLine(contents string) string {
	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line, _, _ = strings.Cut(line, "#")
		return strings.TrimSpace(line)
	}
	return ""
}

func runtimeFinding(source, dir, pointer, name, version, description string) plan.Finding {
	evidence := []plan.Evidence{{
		Kind:        plan.EvidenceInvocation,
		Source:      source,
		Pointer:     pointer,
		Description: description,
	}}
	return requirementFinding(dir, plan.Requirement{
		Kind:       plan.RequirementRuntime,
		Name:       name,
		Version:    version,
		Confidence: plan.ConfidenceHigh,
		Evidence:   evidence,
	})
}

func runFindings(ctx provider.Context, source, dir, pointer, script string, matrix map[string][]string) ([]plan.Finding, error) {
	statements := knowledge.ParseStatementsKeepPipelines(normalizeRunScript(script))
	suffix := len(statements) > 1
	currents := []string{dir}
	var findings []plan.Finding
	for i, stmt := range statements {
		dirs := expandStatementDirectories(ctx.RepositoryRoot, currents, stmt, matrix)
		if stmt.Chdir != "" {
			if len(dirs) > 0 {
				currents = dirs
			}
			continue
		}
		if skipStatement(stmt) {
			continue
		}
		if stmt.Invocation.Executable == "" {
			continue
		}

		commandPointer := pointer
		if suffix {
			commandPointer = pointer + "#command=" + strconv.Itoa(i)
		}
		for _, commandDir := range dirs {
			command, err := observedCommand(source, commandDir, commandPointer, stmt)
			if err != nil {
				return nil, err
			}
			findings = append(findings, plan.CommandFinding{
				ProjectPath: commandDir,
				Detector:    providerName,
				Command:     command,
			})
		}
	}
	return findings, nil
}

func expandStatementDirectories(repo string, currents []string, stmt knowledge.Statement, matrix map[string][]string) []string {
	var dirs []string
	seen := make(map[string]struct{}, len(currents))
	for _, current := range currents {
		for _, dir := range statementDirectories(repo, current, stmt, matrix) {
			if _, ok := seen[dir]; ok {
				continue
			}
			seen[dir] = struct{}{}
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

func statementDirectories(repo, current string, stmt knowledge.Statement, matrix map[string][]string) []string {
	if stmt.Chdir != "" {
		return expandDirectories(repo, current, stmt.Chdir, matrix)
	}
	rel := stmt.WorkingDir
	if rel == "" {
		rel, _ = knowledge.StripDirectoryFlags(stmt.Invocation)
	}
	if rel == "" {
		return []string{current}
	}
	return expandDirectories(repo, current, rel, matrix)
}

func observedCommand(source, dir, pointer string, stmt knowledge.Statement) (plan.Command, error) {
	id, err := plan.NewCommandID(plan.CommandIdentity{
		ProjectPath: dir,
		Provider:    providerName,
		Source:      source,
		Pointer:     pointer,
	})
	if err != nil {
		return plan.Command{}, err
	}

	_, canonical := knowledge.StripDirectoryFlags(stmt.Invocation)
	run := knowledge.RedactAssignmentValues(stmt.Raw)
	return plan.Command{
		ID:              id,
		Name:            knowledge.CommandName(canonical),
		Run:             stringPtr(run),
		Directory:       dir,
		Scope:           plan.ScopeProject,
		Origin:          plan.CommandObserved,
		Confidence:      plan.ConfidenceHigh,
		Evidence:        []plan.Evidence{invocationEvidence(source, pointer)},
		Interpretations: observedInterpretations(source, pointer, canonical),
		Variants:        []plan.CommandVariant{},
	}, nil
}

func observedInterpretations(source, pointer string, inv knowledge.Invocation) []plan.Interpretation {
	matches := knowledge.Interpret(inv)
	classified, ok := knowledge.ClassifyManager(inv)
	if ok && classified.Install && !hasCapability(matches, plan.CapabilityDependenciesInstall) {
		matches = append(matches, knowledge.Match{
			Capability:  plan.CapabilityDependenciesInstall,
			Confidence:  plan.ConfidenceHigh,
			Description: "The command installs package-manager dependencies.",
		})
	}
	if len(matches) == 0 {
		return []plan.Interpretation{}
	}
	interpretations := make([]plan.Interpretation, 0, len(matches))
	for _, match := range matches {
		interpretations = append(interpretations, plan.Interpretation{
			Capability: match.Capability,
			Confidence: match.Confidence,
			Evidence: []plan.Evidence{{
				Kind:        plan.EvidenceInvocation,
				Source:      source,
				Pointer:     pointer,
				Description: match.Description,
			}},
		})
	}
	return interpretations
}

func hasCapability(matches []knowledge.Match, capability plan.Capability) bool {
	for _, match := range matches {
		if match.Capability == capability {
			return true
		}
	}
	return false
}

func skipStatement(stmt knowledge.Statement) bool {
	raw := strings.TrimSpace(stmt.Raw)
	if raw == "" {
		return true
	}
	if strings.Contains(raw, "$(") || strings.Contains(raw, "=(") || strings.Contains(raw, "< <(") {
		return true
	}
	if _, ok := heredocDelimiter(raw); ok {
		return true
	}

	name := stmt.Invocation.Executable
	if name == "" {
		tokens := strings.Fields(raw)
		if len(tokens) == 0 {
			return true
		}
		name = tokens[0]
	}
	if implausibleExecutable(name) {
		return true
	}
	if (name == "bash" || name == "sh") && len(stmt.Invocation.Args) == 0 {
		return true
	}
	if knowledge.IsGlobalInstall(stmt.Invocation) {
		return true
	}
	if knowledge.IsRemoteGoInstall(stmt.Invocation) || knowledge.IsRemoteGemInstall(stmt.Invocation) || knowledge.IsSystemPackagePlumbing(stmt.Invocation) || knowledge.IsToolPlumbing(stmt.Invocation) {
		return true
	}
	if knowledge.HasUnclosedGHAExpression(raw) {
		return true
	}
	if knowledge.IsBareUnixTest(stmt) {
		return true
	}
	_, skip := skippedExecutables[name]
	return skip
}

func implausibleExecutable(name string) bool {
	if name == "" || strings.HasPrefix(name, "-") {
		return true
	}
	if strings.ContainsAny(name, "(){}[]") {
		return true
	}
	if name == "#" || strings.HasPrefix(name, "#") {
		return true
	}
	switch name {
	case "const", "let", "var", "else", "NODE", "EOF", "END":
		return true
	default:
		return false
	}
}

var skippedExecutables = map[string]struct{}{
	"set": {}, "unset": {}, "export": {}, "source": {}, ".": {},
	"[": {}, "[[": {}, "exit": {}, "return": {}, "wait": {}, "trap": {},
	"alias": {}, "declare": {}, "local": {}, "readonly": {}, "typeset": {},
	"let": {}, "builtin": {}, "hash": {}, "ulimit": {}, "umask": {},
	"if": {}, "then": {}, "fi": {}, "else": {}, "elif": {}, "for": {},
	"while": {}, "until": {}, "do": {}, "done": {}, "case": {}, "esac": {},
	"function": {}, "select": {}, "time": {}, "coproc": {},
	"true": {}, "false": {}, "echo": {}, "printf": {}, "read": {}, "readarray": {},
	"mapfile": {}, "yes": {}, "cat": {}, "tee": {},
	"continue": {}, "break": {}, "shift": {}, "!": {},
	"cp": {}, "mv": {}, "rm": {}, "mkdir": {}, "rmdir": {}, "ln": {},
	"find": {}, "sed": {}, "awk": {}, "grep": {}, "diff": {},
	"sort": {}, "uniq": {}, "xargs": {}, "tr": {}, "cut": {},
	"head": {}, "tail": {}, "ls": {}, "chmod": {}, "chown": {},
	"touch": {}, "install": {}, "pushd": {}, "popd": {},
	"dirname": {}, "basename": {}, "realpath": {}, "pwd": {},
	"which": {}, "command": {}, "type": {},
	"git": {}, "curl": {}, "wget": {},
	"env": {}, "ssh": {}, "rsync": {},
	"udevadm": {},
}

func envFindings(source, pointer, dir string, env stringMap, keepLiterals bool) []plan.Finding {
	if len(env) == 0 {
		return nil
	}
	names := make([]string, 0, len(env))
	secret := make(map[string]bool, len(env))
	for _, name := range sortedKeys(env) {
		value := env[name]
		isSecret := isSecretValue(value)
		if skipEnvName(name) || skipEnvValue(value) {
			continue
		}
		if !isSecret && !keepLiterals {
			continue
		}
		names = append(names, name)
		secret[name] = isSecret
	}
	return envRequirementFindings(source, pointer, dir, names, secret)
}

func envRequirementFindings(source, pointer, dir string, names []string, secret map[string]bool) []plan.Finding {
	findings := make([]plan.Finding, 0, len(names))
	for _, name := range names {
		isSecret := secret[name]
		findings = append(findings, requirementFinding(dir, plan.Requirement{
			Kind:       plan.RequirementEnvironment,
			Name:       name,
			IsRequired: boolPtr(isSecret),
			HasDefault: boolPtr(!isSecret),
			Confidence: plan.ConfidenceHigh,
			Evidence: []plan.Evidence{{
				Kind:    plan.EvidenceInvocation,
				Source:  source,
				Pointer: pointer + "/" + escapePointer(name),
			}},
		}))
	}
	return findings
}

func skipEnvName(name string) bool {
	if name == "CI" {
		return true
	}
	return strings.HasPrefix(name, "GITHUB_") || strings.HasPrefix(name, "RUNNER_")
}

func skipEnvValue(value string) bool {
	if isSecretValue(value) {
		return false
	}
	return strings.Contains(value, "${{")
}

func isSecretValue(value string) bool {
	rest := value
	for {
		start := strings.Index(rest, "${{")
		if start < 0 {
			return false
		}
		rest = rest[start:]
		end := strings.Index(rest, "}}")
		if end < 0 {
			return false
		}
		if strings.Contains(rest[:end], "secrets.") {
			return true
		}
		rest = rest[end+2:]
	}
}

func serviceFindings(source, jobPointer, dir string, services map[string]service, matrix map[string][]string) []plan.Finding {
	var findings []plan.Finding
	for _, name := range sortedKeys(services) {
		svc := services[name]
		imagePointer := jobPointer + jsonPointer("services", name, "image")
		images := resolveServiceImages(svc.Image, matrix)
		if len(images) == 0 {
			findings = append(findings, matrixServiceFact(source, imagePointer, dir, name, svc.Image))
		} else {
			for _, image := range images {
				serviceName, version := splitImage(image)
				if serviceName == "" || strings.Contains(serviceName, "${{") {
					findings = append(findings, matrixServiceFact(source, imagePointer, dir, name, image))
					continue
				}
				findings = append(findings, requirementFinding(dir, plan.Requirement{
					Kind:       plan.RequirementService,
					Name:       serviceName,
					Version:    version,
					Confidence: plan.ConfidenceHigh,
					Evidence: []plan.Evidence{{
						Kind:    plan.EvidenceInvocation,
						Source:  source,
						Pointer: imagePointer,
					}},
				}))
			}
		}
		findings = append(findings, envFindings(source, jobPointer+jsonPointer("services", name, "env"), dir, svc.Env, false)...)
	}
	return findings
}

func resolveServiceImages(image string, matrix map[string][]string) []string {
	image = strings.TrimSpace(image)
	if image == "" {
		return nil
	}
	if axis, ok := matrixAxis(image); ok {
		if values := matrix[axis]; len(values) > 0 {
			return values
		}
		return nil
	}
	if strings.Contains(image, "${{") {
		return nil
	}
	return []string{image}
}

func matrixServiceFact(source, pointer, dir, service, image string) plan.Finding {
	value := strings.TrimSpace(image)
	if axis, ok := matrixAxis(image); ok {
		value = axis
	}
	return plan.PropertyFinding{
		ProjectPath: dir,
		Detector:    providerName,
		Property: plan.Property{
			Kind:       plan.PropertyFact,
			Name:       "ci.matrix." + service,
			Value:      value,
			Confidence: plan.ConfidenceHigh,
			Evidence: []plan.Evidence{{
				Kind:    plan.EvidenceInvocation,
				Source:  source,
				Pointer: pointer,
			}},
		},
	}
}

func splitImage(image string) (string, string) {
	image = strings.TrimSpace(image)
	if image == "" {
		return "", ""
	}
	image = strings.TrimPrefix(image, "docker://")
	name, digest, _ := strings.Cut(image, "@")
	slash := strings.LastIndex(name, "/")
	repo := name
	if slash >= 0 {
		repo = name[slash+1:]
	}
	repoName, tag, found := strings.Cut(repo, ":")
	if !found {
		return path.Base(repoName), digest
	}
	if digest != "" {
		return repoName, digest
	}
	return repoName, tag
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func limitationFinding(source, pointer, value, description string) plan.Finding {
	return plan.PropertyFinding{
		ProjectPath: ".",
		Detector:    providerName,
		Property: plan.Property{
			Kind:       plan.PropertyFact,
			Name:       "provider.github-actions.limitation",
			Value:      value,
			Confidence: plan.ConfidenceHigh,
			Evidence: []plan.Evidence{{
				Kind:        plan.EvidenceInvocation,
				Source:      source,
				Pointer:     pointer,
				Description: description,
			}},
		},
	}
}

func requirementFinding(dir string, requirement plan.Requirement) plan.Finding {
	return plan.RequirementFinding{
		ProjectPath: dir,
		Detector:    providerName,
		Requirement: requirement,
	}
}

func invocationEvidence(source, pointer string) plan.Evidence {
	return plan.Evidence{
		Kind:    plan.EvidenceInvocation,
		Source:  source,
		Pointer: pointer,
	}
}

func stringPtr(value string) *string {
	return &value
}

func boolPtr(value bool) *bool {
	return &value
}
