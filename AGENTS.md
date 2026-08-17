# Agent guide

Suss statically inspects a repository and emits an evidence-backed plan for setting it up, building, testing, and running it. It never executes repository commands.

Read these before implementing anything:

- `idea.md` — the design. Do not contradict it silently; if a milestone proves it wrong, stop and say so.
- `plan.md` — build order and current status. Work on the active milestone only. When a milestone is completed and approved, record the decisions made during review in its Status line, as earlier milestones do.

## The gate

`make check` (format, lint, race tests, module tidiness, vulnerability scan) must pass before you consider any change done. CI enforces the same targets. Do not add `//nolint` comments; if a lint rule fights reasonable code, disable that rule in `.golangci.yml` with a one-line justification.

## Output contract

The versioned JSON document is the product; everything else renders or conforms to it.

- The schema lives in `schema/plan.v1.schema.json`; the Go types in `plan/`. Changing either requires changing both, plus the examples in `schema/examples/`.
- `run: null` is legal only on a declared command that is referenced by an ambiguity via `commandId`. `plan.Document.Validate` enforces this at emit time.
- All output arrays have canonical order, implemented in `plan/sort.go`. Never rely on map iteration or insertion order for output stability.
- Command IDs hash source identity (project path, provider, source, pointer) and exclude the run text.

## Honesty rules

- Every finding carries evidence pointing at real files. Never fabricate or pad evidence; never attach evidence that asserts a different value than the finding reports.
- A command Suss cannot interpret is reported without interpretation. No guessing to make output look complete.
- Never expose secret values. Environment variable names only.

## Golden corpus

- `go test .` compares detection output byte-for-byte against `testdata/golden/`. Regenerate with `go test . -update`, then review the diff as carefully as code. Never edit golden files by hand.
- Remote corpus repositories are pinned by commit SHA in `corpus_test.go` and shallow-fetched into `testdata/cache/` (gitignored). Pin exact SHAs, never branches.
- Each milestone adds corpus repositories per `plan.md`. Fixtures for narrow cases live in `testdata/fixtures/`.

## Conventions

- Providers implement `provider.Provider` and are registered in `detect.go`. They emit findings; `assemble.go` shapes them into the plan. Cross-provider reconciliation is a separate layer (milestone 3), not provider logic.
- Tool/invocation knowledge is data in `knowledge/invocations.json`, not code branches.
- The human renderer (`render/`) reads only the JSON document plus provider names. It must never inspect the repository.
- Tests are standard library only — no assertion or mocking libraries. Follow the existing style: focused test functions with literal fixtures, `t.Parallel()` where safe.
