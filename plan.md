# Suss v0 development plan

_Made with Fable 5._

This plan turns [idea.md](idea.md) into ordered milestones. Read that document first; this one does not restate the design, only the build order.

## Principles

- **Risk first.** Reconciliation — matching CI invocations to declared tasks — is the make-or-break part of the design. It is proven by milestone 3, not bolted on at the end.
- **Corpus-driven.** Every milestone ends with the golden-plan snapshot harness passing on more corpus repositories. The harness exists from milestone 1, even with a single repository in it.
- **Schema before providers.** The versioned JSON output is the primary product. Its shape is pinned down first, and everything else conforms to it.
- **Honest output over clever output.** At every milestone, a command Suss cannot interpret is reported without interpretation. No guessing to make demos look better.

## Milestone 1 — Skeleton and contract — DONE

Establish the Go module, the output contract, and the test harness.

This milestone is executed in two phases with a human review checkpoint between them. The JSON schema and core types are the contract everything else inherits; an agent designing them needs to be supervised to avoid making dozens of small, plausible-looking decisions (enum spellings, how ambiguity nests, optional vs. required fields, the ID format) that are cheap to review now but expensive to unwind after several providers conform to them. The rest of the milestone is mechanical and safe to implement in one pass.

### Phase 1a — Contract design (stop for human review) — DONE

Scope:

- Core types: `Finding`, `Evidence`, `Command`, `Requirement`, `ProjectPlan`.
- The concrete JSON schema: field-by-field contract, confidence enum, deterministic command ID scheme, ambiguity/conflict representation, schema version field. Published as JSON Schema.
- Two or three realistic example plan documents (hand-written, e.g. modeled on the idea.md example repository) demonstrating variants, inferred commands, ambiguity, and conflicts.

The phase ends with the schema, types, and examples presented for review. No further implementation until the contract is approved.

Status: completed and approved after one review iteration. Notable contract decisions made during review: `run` is nullable and `null` is legal only on declared commands whose invocation is ambiguous; ambiguities and conflicts carry an optional `commandId` link; command IDs hash the source identity (project path, provider, source, pointer) and deliberately exclude the run text.

### Phase 1b — Skeleton implementation — DONE

Scope:

- Go module, repository layout, CI for Suss itself.
- Project-root discovery (manifest-based: `package.json`, `go.mod`, `mix.exs`).
- `suss . --json` producing a valid (if nearly empty) plan.
- Golden-corpus snapshot harness: corpus repositories pinned by commit, expected plans checked in, `go test` compares detection output against them.

Carry-overs from the phase 1a review (do not skip):

- Add a Go test that validates every document in `schema/examples/` against `schema/plan.v1.schema.json` (e.g. `santhosh-tekuri/jsonschema`). Go decoding alone is weaker than the schema and permits drift; during review this validation was only performed ad hoc outside the test suite.
- Add an emit-time validity check for the invariant JSON Schema cannot express: a command with `run: null` must be referenced by an ambiguity via `commandId`. Currently this is only documented on the `Command` type.
- Define and document canonical sort ordering for all arrays in the output (projects, commands, evidence, and so on). Golden snapshots require byte-stable output.
- The tests in `plan/types_test.go` have never been executed: there is no `go.mod` yet and the development machine had no Go toolchain during review. The hard-coded command IDs were verified out-of-band against an independent hash implementation, but running the suite is the first order of business once the module exists.
- `TestNewDocumentInitializesRequiredCollections` asserts the marshaled JSON contains no `"null"` substring. This only holds because it marshals an empty project; a legitimate `run: null` would trip it. Tighten the assertion when extending the test.

Acceptance:

- `suss . --json` on a fixture repository emits schema-valid output.
- The harness runs in CI with at least one fixture repository.

Status: completed. The Go module, project-root discovery, canonical JSON emit, schema-validation tests, emit-time `run: null` check, and snapshot harness are in place. Human-readable rendering of the full plan remains milestone 2.

### Decisions to pin before prompting an agent

These are not recorded in idea.md and an agent would otherwise guess them silently. Recommended defaults, to be confirmed:

- **Go module path**: `github.com/superplanehq/suss`.
- **Minimum Go version**: latest stable at the time of scaffolding.
- **CI for Suss itself**: GitHub Actions (decided). A useful side effect: Suss's own repository becomes a natural GHA corpus candidate later, so its workflow files should stay clean and conventional.
- **License**: Apache 2.0 (decided). It is the norm for company-backed Go developer tooling (Buildpacks, Caddy, Cobra, CNCF projects), and its explicit patent grant makes corporate adoption and contribution easier than MIT for the agent/CI audience Suss targets.

## Milestone 2 — Node provider and human renderer — DONE

First end-to-end useful output.

Scope:

- Node provider: `package.json` scripts, `packageManager` field, lockfile detection (npm/pnpm/yarn/bun), competing-lockfile ambiguity reporting, runtime version files (`.nvmrc`, `.node-version`, `engines`).
- Initial declarative knowledge base (data file): npm/pnpm/yarn/bun invocations, `vitest`, `jest`, `eslint`, `tsc`, `prettier`, `vite`.
- Tool-configuration detection for the same tools (configured-but-no-command reporting).
- Human-readable renderer for `suss .`, implemented strictly as a renderer of the JSON. The renderer must distinguish "the repository has no detectable content" from "no provider covers this yet", and say which providers ran. A wall of empty fields with no explanation reads as a broken tool.
- Fixture-like project roots are reported, not hidden. Path segments `testdata`, `fixtures`, and `__fixtures__` attach an evidence-backed `project.role=fixture` fact at high confidence; `examples` uses medium confidence because real packages sometimes live there. Large corpus repositories (grafana) will stress this.
- Remote corpus fetching. The milestone 1 harness only supports local fixtures; `corpusEntry` already has `gitURL` and `commit` fields but `repository()` fails on them. This milestone implements that path, since `chalk/chalk` is the first remote corpus entry: pin an exact commit SHA (not a branch), shallow-fetch it into `testdata/cache`, reuse the cache across runs, and gitignore the cache directory. A fresh checkout and CI must be able to run the corpus test with no manual cloning.

Acceptance:

- Golden plan for `chalk/chalk` is correct: install, test, lint commands with evidence.

Status: completed and approved after one review iteration. Notable decisions: fixture-like roots (`testdata`, `fixtures`, `__fixtures__` high confidence; `examples` medium) are reported as ordinary projects with an evidence-backed `project.role=fixture` fact, not hidden. Script invocations use `pnpm run` / `yarn run` rather than bare `pnpm <name>` / `yarn <name>`, so builtin subcommands are not shadowed. `engines.node` is merged into a `.nvmrc` / `.node-version` pin only when the version strings are equal; otherwise it is a separate requirement.

## Milestone 3 — GitHub Actions provider and reconciliation (make-or-break) — DONE

Prove the core design against real workflow files.

Scope:

- GitHub Actions provider: jobs, steps, `working-directory`, `setup-node`/`setup-go` version evidence, matrix builds, env var names, service containers. Reusable workflows and composite actions may be deferred, but deferral must be recorded as a known limitation.
- Reconciliation layer: grouping findings, matching CI invocations to declared tasks (shell parsing for `cd X && ...` and env prefixes), variant recording, conflict recording, confidence assignment.
- Written matching rules: when is a CI command a variant of a declared task, when is it unrelated, and what happens when no link can be established. This design work happens here, against fixtures, not in the abstract.
- Workspace-orchestrator detection as project facts (`pnpm-workspace.yaml`, `turbo.json`, `nx.json`, yarn workspaces); repository-wide scope on root commands; no fan-out.

Acceptance:

- Golden plan for `excalidraw/excalidraw` is correct: workspace facts, root scripts, CI variants linked to declared tasks, unlinked CI commands reported honestly.
- If the reconciliation model does not survive contact with excalidraw's workflows, stop and revise idea.md before proceeding.
- Extra corpus pin: `mermaid-js/mermaid`. A messy GHA-heavy pnpm workspace. The basics match the contributing guide (pnpm, `pnpm install`, `pnpm test`, `pnpm run build`, `pnpm run dev`); remaining declared scripts and uninterpreted CI tools are listed without guessing. CI unix/VCS plumbing is not a plan command. Environment names are CI secrets plus workflow-level literals, not job wiring.

Status: completed and approved after two review iterations. The reconciliation model survived excalidraw and mermaid; idea.md gained refinements (which CI run steps are repository commands, which env names are requirements) rather than revisions. The matching rules live as the `reconcile` package doc comment. Notable decisions made during review:

- Workflow YAML maps (jobs, services, env) are iterated in sorted order; duplicate evidence is merged by identity (kind, source, pointer, description), never dropped. A regression test detects the same repository twice and compares bytes.
- Observed CI commands never match other observed commands; identical observations from distinct steps are tolerated as separate commands.
- Workspace membership is decided by the declared package globs (yarn/npm `workspaces`, `pnpm-workspace.yaml`), not by ancestor presence. Non-member nested projects keep their own install commands and inherit manager signals only from a genuine workspace ancestor.
- Declared version ranges are evaluated for real (`||` unions, comparators, caret, tilde, wildcard). Unevaluable versions — expressions, aliases like `lts/*`, and hyphen ranges (known limitation) — become `ci.matrix.<runtime>` facts, never supporting evidence or conflicts.
- Heredoc statements, shell/unix/VCS plumbing, and `curl`/`wget` fetches are not plan commands. Unresolved `setup-*` version expressions yield unversioned runtime requirements. Expression-valued working directories fall back to the enclosing scope; matrix-valued ones fan out per axis value.
- Leading `NAME=value` prefixes in observed run text are redacted to `NAME=$NAME` so command text never carries assignment values.
- Reusable workflows and local composite actions are detected but not expanded, recorded as `provider.github-actions.limitation` facts.

## Milestone 4 — Go provider and convention inference — DONE

The opposite evidence regime: few declared tasks, strong conventions.

Scope:

- Go provider: `go.mod` (module, Go version), test file presence, `go.work` as workspace fact.
- Convention-based inference (`go test ./...`, `go build`, `go vet`) with explicit inferred status, source convention, and confidence.
- golangci-lint config detection; knowledge base entries for the Go toolchain.

Acceptance:

- Golden plans for `spf13/cobra` and `caddyserver/caddy` are correct, including inferred commands marked as inferred and CI cross-confirmation raising confidence.

Status: completed. The Go provider and cobra/caddy goldens are in. Inferred commands stay `origin: inferred` with `go-ecosystem` convention evidence; CI confirmation attaches a variant and raises confidence. Notable decisions:

- `go test ./...` is inferred only when `*_test.go` files exist (high confidence). `go build ./...` and `go vet ./...` are inferred from `go.mod` alone at medium; `go mod download` is inferred preparation at medium.
- `go.work` is a `workspace.orchestrator=go` fact. golangci-lint config is `tool.configured`, not an inferred command; `golangci/golangci-lint-action` is an observed `golangci-lint run`.
- Non-package-manager matching ignores flags, so `go test ./...` links to `go test -race ./...`. Test files under a nested `go.mod` do not count for the parent module.
- Remote `go install`, `go env`/`go version`, and `env`/`ssh`/`rsync` are CI plumbing, not plan commands.
- Cobra's `make richtest` is reported uninterpreted (Make is milestone 5). Caddy's `go test -short -race ./...` is a CI variant of the inferred test command.

## Milestone 5 — Make, Compose, and requirements

Cross-cutting sources and the requirements model.

Scope:

- Make provider: target enumeration, recipe text as evidence (no variable expansion beyond simple cases; recorded as a limitation).
- Docker Compose provider: services as requirements, service commands as preparation candidates.
- Requirements assembly: runtimes, tools, services, environment variable names (from CI env, `.env.example`, compose) with the never-expose-secret-values rule.
- Preparation command detection (install steps, `docker compose up -d ...`).

Acceptance:

- Golden plan for `plausible/analytics` is substantially correct on the Node/Make/Compose/requirements side (Elixir commands arrive in milestone 6).
- Extra corpus pin: `knadh/listmonk`. Official setup is `docker compose up -d` (app + postgres). Make, Go, a Node frontend, and `.env.sample` are also present. The `dev/` compose file is the local stack (postgres, mailhog, adminer).

Status: review fixes applied; awaiting approval. `plausible/analytics` at the pinned SHA ships no Compose file and no `.env.example`, so Compose / env-file acceptance is carried by `knadh/listmonk`. Notable decisions from the milestone 5 review:

- Declared wrappers win over inferred convention commands of the same capability (`make test` drops `go test ./...`). This is `reconcile.PreferDeclared`, not provider logic. idea.md: conventions fill gaps when explicit evidence is absent.
- Any command interpreted as `dependencies.install` is preparation, regardless of detector or origin. The previous `Detector == "make"` special case is gone. `docker compose up` remains preparation.
- Tool version/info/help probes are CI plumbing, matching the milestone 4 `go version` / `go env` rule: `docker version` / `docker info` / `docker compose version`, and `--version` / `-v` on node/npm/pnpm/yarn/bun/python/ruby/java. `npm version` / `yarn version` bump the package and stay commands. `make version` is a repository target, not plumbing.
- Renderer polish (command directory when it differs from the project path; requirement kind on each line) is in this milestone, not deferred.

## Milestone 6 — Elixir provider, Semaphore provider, dogfood

Close the loop on our own repositories.

Scope:

- Elixir provider: `mix.exs`, Mix tasks and aliases, `.tool-versions`, ExUnit, Credo/Dialyzer configs; knowledge base entries.
- Semaphore provider: pipelines, blocks, jobs, working directories, version and service evidence — same reconciliation path as GitHub Actions.
- Whatever gaps the dogfood repositories expose.

Acceptance:

- Golden plans for `elixir-ecto/ecto`, then `superplanehq/superplane`, `operately/operately`, and `semaphoreio/semaphore` are correct end to end.

## Milestone 7 — Polish and v0 release

Scope:

- `suss explain <capability>`: show the chosen command for a capability with its full evidence chain and alternatives.
- `suss list`: enumerate all discovered commands across projects.
- `grafana/grafana` added to the corpus as the stress test; fix what it breaks or record limitations.
- Schema review and freeze for v0; document the compatibility policy.
- README, distribution (release binaries), library API (`detect(path) → []ProjectPlan`) documented.

Acceptance:

- Full corpus green in CI. Tagged v0 release with binaries.

## Out of scope for v0

Per idea.md: command execution, execution profiles, behavior characteristics, README/docs parsing, workspace fan-out modeling, Python/Ruby/Rust/JVM/.NET providers, GitLab/CircleCI/Buildkite.
