package semaphore

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/superplanehq/suss/knowledge"
	"github.com/superplanehq/suss/plan"
	"github.com/superplanehq/suss/provider"
)

func extractPipeline(ctx provider.Context, source string, pipeline pipelineFile) (provider.Result, error) {
	var result provider.Result
	result.Findings = append(result.Findings, environmentFindings(source, "/global_job_config/env_vars", ".", pipeline.GlobalJobConfig.EnvVars)...)

	globalDir, findings, err := extractCommands(ctx, source, ".", "/global_job_config/prologue/commands", pipeline.GlobalJobConfig.Prologue.Commands, nil)
	if err != nil {
		return provider.Result{}, err
	}
	result.Findings = append(result.Findings, findings...)

	for blockIndex, block := range pipeline.Blocks {
		blockPointer := jsonPointer("blocks", strconv.Itoa(blockIndex), "task")
		blockFindings, err := extractBlock(ctx, source, globalDir, blockPointer, block)
		if err != nil {
			return provider.Result{}, err
		}
		result.Findings = append(result.Findings, blockFindings...)
	}

	for _, group := range pipeline.GlobalJobConfig.Epilogue.commandGroups() {
		_, epilogueFindings, err := extractCommands(ctx, source, globalDir, "/global_job_config/epilogue/"+group.pointer, group.commands, nil)
		if err != nil {
			return provider.Result{}, err
		}
		result.Findings = append(result.Findings, epilogueFindings...)
	}

	// Semaphore documents that global_job_config does not apply to after-pipeline
	// jobs, so this task starts at the checked-out repository root independently.
	afterFindings, err := extractTask(ctx, source, ".", "/after_pipeline/task", pipeline.AfterPipeline.Task)
	if err != nil {
		return provider.Result{}, err
	}
	result.Findings = append(result.Findings, afterFindings...)
	return result, nil
}

func extractBlock(ctx provider.Context, source, globalDir, pointer string, block block) ([]plan.Finding, error) {
	return extractTask(ctx, source, globalDir, pointer, block.Task)
}

func extractTask(ctx provider.Context, source, base, pointer string, task task) ([]plan.Finding, error) {
	var findings []plan.Finding
	taskDir, prologueFindings, err := extractCommands(ctx, source, base, pointer+"/prologue/commands", task.Prologue.Commands, nil)
	if err != nil {
		return nil, err
	}
	findings = append(findings, prologueFindings...)

	for jobIndex, job := range task.Jobs {
		jobPointer := pointer + jsonPointer("jobs", strconv.Itoa(jobIndex))
		jobFindings, err := extractJob(ctx, source, taskDir, jobPointer, job)
		if err != nil {
			return nil, err
		}
		findings = append(findings, jobFindings...)
	}
	for _, group := range task.Epilogue.commandGroups() {
		_, epilogueFindings, err := extractCommands(ctx, source, taskDir, fmt.Sprintf("%s/epilogue/%s", pointer, group.pointer), group.commands, nil)
		if err != nil {
			return nil, err
		}
		findings = append(findings, epilogueFindings...)
	}
	return findings, nil
}

func extractJob(ctx provider.Context, source, base, pointer string, job job) ([]plan.Finding, error) {
	matrix := matrixValues(job.Matrix)
	_, commandFindings, err := extractCommands(ctx, source, base, pointer+"/commands", job.Commands, matrix)
	if err != nil {
		return nil, err
	}
	return commandFindings, nil
}

func extractCommands(ctx provider.Context, source, base, pointer string, commands []string, matrix map[string][]string) (string, []plan.Finding, error) {
	currents := []string{base}
	var findings []plan.Finding
	for commandIndex, script := range commands {
		if isComplexShellScript(script) {
			continue
		}
		commandPointer := pointer + "/" + strconv.Itoa(commandIndex)
		statements := knowledge.ParseStatementsKeepPipelines(script)
		for statementIndex, statement := range statements {
			statementPointer := commandPointer
			if len(statements) > 1 {
				statementPointer += "#command=" + strconv.Itoa(statementIndex)
			}
			dirs := expandStatementDirectories(ctx.RepositoryRoot, currents, statement, matrix)
			if statement.Chdir != "" {
				if len(dirs) > 0 {
					currents = dirs
				}
				continue
			}
			for _, dir := range dirs {
				if statement.Invocation.Executable == "sem-version" {
					findings = append(findings, versionEvidence(source, dir, statementPointer, statement.Invocation, matrix)...)
					continue
				}
				if statement.Invocation.Executable == "sem-service" {
					findings = append(findings, serviceEvidence(source, dir, statementPointer, statement.Invocation, matrix)...)
					continue
				}
				if skipSemaphoreStatement(statement) {
					continue
				}
				command, err := observedCommand(source, dir, statementPointer, statement)
				if err != nil {
					return base, nil, err
				}
				findings = append(findings, plan.CommandFinding{ProjectPath: dir, Detector: providerName, Command: command})
			}
		}
	}
	if len(currents) == 0 {
		return base, findings, nil
	}
	return currents[0], findings, nil
}

func expandStatementDirectories(repo string, currents []string, stmt knowledge.Statement, matrix map[string][]string) []string {
	rel := stmt.Chdir
	if rel == "" {
		rel = stmt.WorkingDir
	}
	if rel == "" {
		return append([]string{}, currents...)
	}
	var dirs []string
	seen := make(map[string]struct{}, len(currents))
	for _, current := range currents {
		for _, dir := range expandSemaphoreDirectories(repo, current, rel, matrix) {
			if _, ok := seen[dir]; ok {
				continue
			}
			seen[dir] = struct{}{}
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

func expandSemaphoreDirectories(repo, base, rel string, matrix map[string][]string) []string {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return []string{base}
	}
	if isSemaphoreVar(rel) {
		values := expandMatrixValue(rel, matrix)
		if len(values) == 0 {
			return []string{base}
		}
		dirs := make([]string, 0, len(values))
		for _, value := range values {
			if isSemaphoreVar(value) {
				continue
			}
			dirs = append(dirs, resolveDirectory(repo, base, value))
		}
		if len(dirs) == 0 {
			return []string{base}
		}
		return dirs
	}
	return []string{resolveDirectory(repo, base, rel)}
}

func isSemaphoreVar(raw string) bool {
	return strings.HasPrefix(strings.TrimSpace(raw), "$")
}

// A command containing shell functions, conditionals, subshells, or
// continuations is a program rather than a list of repository invocations.
// Suss leaves it uninterpreted instead of fabricating commands from its body.
func isComplexShellScript(script string) bool {
	trimmed := strings.TrimSpace(script)
	for _, prefix := range []string{"if ", "for ", "while ", "until ", "case ", "function "} {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	markers := []string{"() {", "; then", "; do"}
	if strings.Contains(script, "\n") {
		markers = append(markers, "if ", "then\n", "fi\n", "for ", "while ", "case ", "$(", "\\\n", "<<")
	}
	for _, marker := range markers {
		if strings.Contains(script, marker) {
			return true
		}
	}
	return false
}

func versionEvidence(source, directory, pointer string, invocation knowledge.Invocation, matrix map[string][]string) []plan.Finding {
	if len(invocation.Args) < 2 {
		return nil
	}
	runtime := strings.TrimSpace(invocation.Args[0])
	if _, known := semaphoreRuntimes[runtime]; !known {
		return nil
	}
	versions := expandMatrixValue(invocation.Args[1], matrix)
	if len(versions) == 0 {
		versions = []string{""}
	}
	findings := make([]plan.Finding, 0, len(versions))
	for _, version := range versions {
		findings = append(findings, requirementFinding(directory, plan.Requirement{
			Kind:       plan.RequirementRuntime,
			Name:       runtime,
			Version:    version,
			Confidence: plan.ConfidenceHigh,
			Evidence:   []plan.Evidence{invocationEvidence(source, pointer, "Semaphore selects this runtime for the CI job.")},
		}))
	}
	return findings
}

var semaphoreRuntimes = map[string]struct{}{
	"elixir": {}, "erlang": {}, "go": {}, "java": {}, "node": {}, "php": {}, "python": {}, "ruby": {},
}

func serviceEvidence(source, directory, pointer string, invocation knowledge.Invocation, matrix map[string][]string) []plan.Finding {
	if len(invocation.Args) < 2 || invocation.Args[0] != "start" {
		return nil
	}
	services := expandMatrixValue(invocation.Args[1], matrix)
	if len(services) == 0 {
		return nil
	}
	versions := []string{""}
	if len(invocation.Args) >= 3 {
		if expanded := expandMatrixValue(invocation.Args[2], matrix); len(expanded) > 0 {
			versions = expanded
		}
	}

	var findings []plan.Finding
	for _, service := range services {
		if service == "" || strings.ContainsAny(service, "$ {}") {
			continue
		}
		for _, version := range versions {
			findings = append(findings, requirementFinding(directory, plan.Requirement{
				Kind:       plan.RequirementService,
				Name:       service,
				Version:    version,
				Confidence: plan.ConfidenceHigh,
				Evidence:   []plan.Evidence{invocationEvidence(source, pointer, "Semaphore starts this service for the CI job.")},
			}))
		}
	}
	return findings
}

func expandMatrixValue(raw string, matrix map[string][]string) []string {
	raw = strings.TrimSpace(raw)
	name := strings.TrimPrefix(raw, "${")
	name = strings.TrimSuffix(name, "}")
	name = strings.TrimPrefix(name, "$")
	if name != raw {
		return slices.Clone(matrix[name])
	}
	if raw == "" {
		return nil
	}
	return []string{raw}
}

func observedCommand(source, directory, pointer string, statement knowledge.Statement) (plan.Command, error) {
	id, err := plan.NewCommandID(plan.CommandIdentity{ProjectPath: directory, Provider: providerName, Source: source, Pointer: pointer})
	if err != nil {
		return plan.Command{}, err
	}
	_, canonical := knowledge.StripDirectoryFlags(statement.Invocation)
	return plan.Command{
		ID:              id,
		Name:            knowledge.CommandName(canonical),
		Run:             stringPtr(knowledge.RedactAssignmentValues(statement.Raw)),
		Directory:       directory,
		Scope:           plan.ScopeProject,
		Origin:          plan.CommandObserved,
		Confidence:      plan.ConfidenceHigh,
		Evidence:        []plan.Evidence{invocationEvidence(source, pointer, "")},
		Interpretations: observedInterpretations(source, pointer, canonical),
		Variants:        []plan.CommandVariant{},
	}, nil
}

func observedInterpretations(source, pointer string, invocation knowledge.Invocation) []plan.Interpretation {
	matches := knowledge.Interpret(invocation)
	interpretations := make([]plan.Interpretation, 0, len(matches))
	for _, match := range matches {
		interpretations = append(interpretations, plan.Interpretation{
			Capability: match.Capability,
			Confidence: match.Confidence,
			Evidence:   []plan.Evidence{invocationEvidence(source, pointer, match.Description)},
		})
	}
	return interpretations
}

func environmentFindings(source, pointer, directory string, variables []envVar) []plan.Finding {
	var findings []plan.Finding
	for index, variable := range variables {
		name := strings.TrimSpace(variable.Name)
		if name == "" || name == "CI" || strings.HasPrefix(name, "SEMAPHORE_") {
			continue
		}
		required := false
		hasDefault := variable.Value != ""
		findings = append(findings, requirementFinding(directory, plan.Requirement{
			Kind:       plan.RequirementEnvironment,
			Name:       name,
			IsRequired: &required,
			HasDefault: &hasDefault,
			Confidence: plan.ConfidenceHigh,
			Evidence:   []plan.Evidence{invocationEvidence(source, pointer+"/"+strconv.Itoa(index)+"/name", "")},
		}))
	}
	return findings
}

func requirementFinding(directory string, requirement plan.Requirement) plan.Finding {
	return plan.RequirementFinding{ProjectPath: directory, Detector: providerName, Requirement: requirement}
}

func invocationEvidence(source, pointer, description string) plan.Evidence {
	return plan.Evidence{Kind: plan.EvidenceInvocation, Source: source, Pointer: pointer, Description: description}
}

func stringPtr(value string) *string {
	return &value
}

func skipSemaphoreStatement(statement knowledge.Statement) bool {
	executable := statement.Invocation.Executable
	if executable == "" || strings.Contains(statement.Raw, "$(") {
		return true
	}
	if knowledge.IsGlobalInstall(statement.Invocation) || knowledge.IsRemoteGoInstall(statement.Invocation) || knowledge.IsRemoteGemInstall(statement.Invocation) || knowledge.IsSystemPackagePlumbing(statement.Invocation) || knowledge.IsToolPlumbing(statement.Invocation) {
		return true
	}
	_, skip := semaphorePlumbing[executable]
	return skip
}

var semaphorePlumbing = map[string]struct{}{
	"checkout": {}, "cache": {}, "artifact": {}, "test-results": {}, "sem-version": {}, "sem-service": {},
	"set": {}, "unset": {}, "export": {}, "source": {}, ".": {}, "[": {}, "[[": {}, "exit": {}, "return": {},
	"if": {}, "then": {}, "fi": {}, "else": {}, "elif": {}, "for": {}, "while": {}, "do": {}, "done": {}, "case": {}, "esac": {},
	"true": {}, "false": {}, "echo": {}, "printf": {}, "read": {}, "cat": {}, "tee": {},
	"cp": {}, "mv": {}, "rm": {}, "mkdir": {}, "rmdir": {}, "ln": {}, "find": {}, "sed": {}, "awk": {}, "grep": {}, "diff": {},
	"sort": {}, "uniq": {}, "xargs": {}, "tr": {}, "cut": {}, "head": {}, "tail": {}, "ls": {}, "chmod": {}, "touch": {},
	"pushd": {}, "popd": {}, "dirname": {}, "basename": {}, "realpath": {}, "pwd": {}, "which": {}, "command": {}, "type": {},
	"git": {}, "curl": {}, "wget": {}, "env": {}, "ssh": {}, "rsync": {}, "sudo": {},
}
