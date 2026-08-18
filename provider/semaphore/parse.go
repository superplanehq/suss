package semaphore

import "gopkg.in/yaml.v3"

type pipelineFile struct {
	Version         string        `yaml:"version"`
	Name            string        `yaml:"name"`
	GlobalJobConfig jobConfig     `yaml:"global_job_config"`
	Blocks          []block       `yaml:"blocks"`
	AfterPipeline   afterPipeline `yaml:"after_pipeline"`
	Promotions      []promotion   `yaml:"promotions"`
}

type block struct {
	Name string `yaml:"name"`
	Task task   `yaml:"task"`
}

type task struct {
	EnvVars  []envVar `yaml:"env_vars"`
	Prologue commands `yaml:"prologue"`
	Jobs     []job    `yaml:"jobs"`
	Epilogue epilogue `yaml:"epilogue"`
}

type jobConfig struct {
	EnvVars  []envVar `yaml:"env_vars"`
	Prologue commands `yaml:"prologue"`
	Epilogue epilogue `yaml:"epilogue"`
}

type job struct {
	Name        string        `yaml:"name"`
	Commands    []string      `yaml:"commands"`
	EnvVars     []envVar      `yaml:"env_vars"`
	Matrix      []matrixEntry `yaml:"matrix"`
	Parallelism int           `yaml:"parallelism"`
}

type commands struct {
	Commands []string `yaml:"commands"`
}

type epilogue struct {
	Commands []string `yaml:"commands"`
	Always   commands `yaml:"always"`
	OnPass   commands `yaml:"on_pass"`
	OnFail   commands `yaml:"on_fail"`
}

type envVar struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

type matrixEntry struct {
	EnvVar string   `yaml:"env_var"`
	Values []string `yaml:"values"`
}

type afterPipeline struct {
	Task task `yaml:"task"`
}

type promotion struct {
	Name         string `yaml:"name"`
	PipelineFile string `yaml:"pipeline_file"`
}

func parsePipeline(contents []byte) (pipelineFile, error) {
	var pipeline pipelineFile
	if err := yaml.Unmarshal(contents, &pipeline); err != nil {
		return pipelineFile{}, err
	}
	return pipeline, nil
}

func (p pipelineFile) isPipeline() bool {
	return p.Version != "" && (len(p.Blocks) > 0 || len(p.AfterPipeline.Task.Jobs) > 0 || len(p.Promotions) > 0)
}

type commandGroup struct {
	pointer  string
	commands []string
}

func (e epilogue) commandGroups() []commandGroup {
	return []commandGroup{
		{pointer: "commands", commands: e.Commands},
		{pointer: "always/commands", commands: e.Always.Commands},
		{pointer: "on_pass/commands", commands: e.OnPass.Commands},
		{pointer: "on_fail/commands", commands: e.OnFail.Commands},
	}
}

func matrixValues(entries []matrixEntry) map[string][]string {
	values := make(map[string][]string, len(entries))
	for _, entry := range entries {
		if entry.EnvVar == "" {
			continue
		}
		for _, value := range entry.Values {
			if value != "" {
				values[entry.EnvVar] = appendUnique(values[entry.EnvVar], value)
			}
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
		if _, ok := seen[value]; value == "" || ok {
			continue
		}
		seen[value] = struct{}{}
		existing = append(existing, value)
	}
	return existing
}
