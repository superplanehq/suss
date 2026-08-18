// Package semaphore detects Semaphore Cloud pipeline YAML files.
//
// Semaphore toolbox programs such as checkout, cache, artifact, test-results,
// sem-version, and sem-service configure the hosted job environment. They are
// never emitted as repository commands. sem-version and sem-service are used
// only as CI evidence for runtime and service requirements, as documented at
// https://docs.semaphore.io/using-semaphore/jobs.
package semaphore

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/superplanehq/suss/provider"
)

const providerName = "semaphore"

// Provider detects all pipeline YAML files in .semaphore.
type Provider struct{}

// Name returns the stable provider identifier.
func (Provider) Name() string {
	return providerName
}

// Detect inspects the repository-level .semaphore directory once.
func (Provider) Detect(ctx provider.Context) (provider.Result, error) {
	files, err := pipelineFiles(ctx.RepositoryRoot)
	if err != nil {
		return provider.Result{}, err
	}

	var result provider.Result
	for _, source := range files {
		contents, err := os.ReadFile(filepath.Join(ctx.RepositoryRoot, filepath.FromSlash(source)))
		if err != nil {
			return provider.Result{}, fmt.Errorf("read %s: %w", source, err)
		}
		pipeline, err := parsePipeline(contents)
		if err != nil {
			return provider.Result{}, fmt.Errorf("parse %s: %w", source, err)
		}
		if !pipeline.isPipeline() {
			continue
		}
		extracted, err := extractPipeline(ctx, source, pipeline)
		if err != nil {
			return provider.Result{}, err
		}
		result.Findings = append(result.Findings, extracted.Findings...)
	}
	return result, nil
}

func pipelineFiles(root string) ([]string, error) {
	dir := filepath.Join(root, ".semaphore")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read .semaphore: %w", err)
	}

	var files []string
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		extension := strings.ToLower(filepath.Ext(entry.Name()))
		if extension == ".yml" || extension == ".yaml" {
			files = append(files, ".semaphore/"+entry.Name())
		}
	}
	slices.Sort(files)
	return files, nil
}
