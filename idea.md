# Project idea

## Project purpose

> Given this repository, determine what it is and how a developer or agent should set it up, build it, test it, lint it, and run it.

For a polyglot repository, Suss may return several project plans. It does not need to model dependencies between them.

Suss discovers three kinds of information:

1. **Requirements** — runtimes, tools, services, and environment variables.
2. **Commands** — exact commands declared or used by the repository.
3. **Interpretations** — optional, evidence-backed descriptions of what those commands do.

Suss preserves the repository’s native vocabulary. It does not translate every project into a universal software lifecycle.

## Example

```text
$ suss .

Repository: acme

Project: frontend
Path: frontend/
Language: TypeScript
Framework: React + Vite
Package manager: pnpm 9

Requirements:
  Node.js 22

Preparation:
  corepack enable
  pnpm install --frozen-lockfile

Commands:
  Build                 pnpm build
  Test                  pnpm test
  Test in CI            pnpm test --run
  Lint                  pnpm lint
  Type-check            pnpm typecheck
  Run locally           pnpm dev

Project: backend
Path: backend/
Language: Go
Framework: chi

Requirements:
  Go 1.24
  PostgreSQL
  golangci-lint 2

Preparation:
  go mod download
  docker compose up -d postgres

Commands:
  Build                 go build ./cmd/server
  Test                  go test ./...
  Lint                  golangci-lint run
  Run locally           go run ./cmd/server

Evidence:
  ✓ frontend/package.json
  ✓ frontend/pnpm-lock.yaml
  ✓ frontend/vite.config.ts
  ✓ backend/go.mod
  ✓ backend/cmd/server/main.go
  ✓ compose.yaml
  ✓ .github/workflows/ci.yml
```

The machine-readable equivalent is the library’s primary product.

## The data model

A project plan contains:

- detected project properties;
- environmental requirements;
- repository-native commands;
- command variants;
- optional semantic interpretations;
- supporting evidence;
- ambiguities and conflicts.

For example:

```json
{
  "projects": [
    {
      "path": "frontend",
      "languages": ["typescript"],
      "frameworks": ["react", "vite"],
      "packageManager": {
        "name": "pnpm",
        "version": "9",
        "evidence": [
          {
            "source": "frontend/package.json",
            "field": "packageManager"
          }
        ]
      },
      "requirements": {
        "runtimes": [
          {
            "name": "node",
            "version": "22",
            "evidence": [
              {
                "source": "frontend/.nvmrc"
              }
            ]
          }
        ],
        "tools": [],
        "services": [],
        "environment": []
      },
      "commands": [
        {
          "id": "package-script:test",
          "name": "test",
          "run": "pnpm test",
          "directory": "frontend",
          "source": {
            "file": "frontend/package.json",
            "field": "scripts.test"
          },
          "interpretations": [
            {
              "capability": "test.run",
              "confidence": "explicit"
            }
          ],
          "variants": [
            {
              "profile": "ci",
              "run": "pnpm test --run",
              "source": {
                "file": ".github/workflows/ci.yml",
                "job": "frontend-tests"
              }
            }
          ]
        },
        {
          "id": "package-script:dev",
          "name": "dev",
          "run": "pnpm dev",
          "directory": "frontend",
          "source": {
            "file": "frontend/package.json",
            "field": "scripts.dev"
          },
          "interpretations": [
            {
              "capability": "application.run",
              "profile": "development",
              "confidence": "high",
              "evidence": ["The script invokes the Vite development server."]
            }
          ]
        }
      ]
    }
  ]
}
```

Suss does not require:

- a files-to-components graph;
- semantic tags for every file;
- code dependency analysis;
- code-to-test impact analysis;
- quality scoring;
- command execution during detection.

The only structural discovery required is identifying project roots:

```text
./                    repository root
./frontend            Node.js project
./backend             Go project
./docs                documentation project
```

## Evidence sources and reconciliation

Suss uses several kinds of repository evidence. These sources have different roles and should not be treated as one strict precedence hierarchy.

### 1. Command declarations

Manifests, repository wrappers, scripts, and task runners tell Suss which commands the project deliberately exposes.

Examples include:

```text
package.json
pyproject.toml
Cargo.toml
./gradlew
./mvnw
Makefile
Taskfile.yml
justfile
mise.toml
tox.ini
./scripts/test
```

Commands should be preserved exactly as the repository defines them.

Examples:

```text
make test
./gradlew test
pnpm lint
./scripts/integration-test
```

Suss should not replace `make test` with a reconstructed lower-level command merely because it recognizes the underlying language or test framework.

### 2. Observed usage in CI

CI configuration shows how commands are invoked in specific contexts.

For example:

```yaml
- uses: actions/setup-node@v4
  with:
    node-version: 22

- run: corepack enable
- run: pnpm install --frozen-lockfile
- run: pnpm lint
- run: pnpm test --run --reporter=junit
```

This may provide evidence about:

- runtime and tool versions;
- dependency installation;
- working directories;
- test, lint, type-check, and build commands;
- CI-specific command variants;
- environment variable names;
- required services;
- ordering and prerequisites.

A CI invocation is not necessarily the preferred local invocation.

For example:

```text
Declared task:
  pnpm test

CI invocation:
  pnpm test --run --reporter=junit
```

These are not conflicting commands. The latter is a CI-specific variant of the former.

Suss should initially support GitHub Actions and Semaphore deeply, followed by GitLab CI and potentially CircleCI or Buildkite.

### 3. Tool configuration

Configuration files establish that a tool is present and provide evidence about how it is used.

```text
eslint.config.js       → ESLint
vitest.config.ts       → Vitest
pytest.ini             → pytest
.golangci.yml          → golangci-lint
ruff.toml              → Ruff
tsconfig.json          → TypeScript
```

Tool presence does not always imply that a canonical repository command exists. If Suss detects a configured tool but no command that invokes it, it should report that distinction.

```text
Detected:
  ESLint is configured.

Command:
  No repository-native lint command was found.
```

### 4. Ecosystem conventions

When a repository does not explicitly define how to perform an operation, Suss may infer a command from well-established language, framework, or tool conventions.

Convention-based inference is the lowest-confidence source of command information.

For example:

```text
Go project                 go test ./...
Rust project               cargo test
Python project with pytest pytest
```

Suss must preserve an explicit repository command when one exists. It should not replace `make test` with `go test ./...`, even if the latter is a conventional Go command.

Every inferred command must include:

- the convention or detector that produced it;
- the evidence that made the convention applicable;
- an explicit indication that it was inferred;
- a confidence level;
- any conflicting evidence.

Example:

```text
Test:
  Command: go test ./...
  Status: inferred
  Source: Go ecosystem convention
  Evidence: go.mod and Go test files detected
  Confidence: high
```

If multiple conventions are plausible, Suss should report the ambiguity rather than silently selecting one.

```text
Dependency installation could not be determined confidently.

Detected:
  package-lock.json
  pnpm-lock.yaml

Candidates:
  npm ci
  pnpm install --frozen-lockfile
```

Suss must not execute an inferred command during detection.

## Reconciliation and conflicting evidence

Repositories frequently contain incomplete, contextual, or contradictory information.

For example:

```text
README             npm test
package-lock.json   npm
pnpm-lock.yaml      pnpm
CI                  pnpm test --run
```

Suss should not silently choose one command and pretend certainty.

```text
Test command:
  pnpm test

CI variant:
  pnpm test --run

Confidence: high
Reason:
  The test task is declared in package.json and invoked by CI.

Conflicting evidence:
  README suggests `npm test`.
  Both package-lock.json and pnpm-lock.yaml are present.
```

Reconciliation should consider the role and context of each source:

- declarations establish which commands the repository exposes;
- CI shows which commands are used in a particular automated context;
- configuration confirms which tools are present;
- conventions fill gaps when explicit evidence is absent;
- documentation provides supporting evidence but may be stale (parsing READMEs and docs is out of scope for v0; only structured sources are used).

The preferred answer depends on the question:

```text
“What test task exists?”       → pnpm test
“How does CI run tests?”       → pnpm test --run
“How should I test locally?”   → probably pnpm test
```

Not every disagreement is a conflict. Suss should preserve legitimate variants such as:

- local and CI;
- development and built;
- unit and integration;
- debug and release;
- package-specific and repository-wide.

## Commands are preserved and optionally classified

Suss should preserve every discovered command exactly as it is declared or invoked.

It may attach one or more semantic capabilities so that humans and programs can search for commands by purpose.

The initial normalized capabilities should remain small:

```text
dependencies.install
artifact.build
test.run
code.lint
code.format
code.typecheck
application.run
```

These capabilities are:

- not lifecycle stages;
- not mutually exclusive;
- not replacements for repository task names;
- optional when the command cannot be interpreted confidently.

A command may provide several capabilities.

For example:

```text
mvn verify
  → artifact.build
  → test.run
```

Repository task names such as the following remain task names:

```text
dev
start
serve
check
verify
package
generate
```

Suss should not infer behavior from a task name alone.

### Execution profiles (post-v0)

A command may have a profile describing the context in which it is used.

For example:

```text
pnpm dev
  Capability: application.run
  Profile: development
  Evidence: invokes the Vite development server

pnpm start
  Capability: application.run
  Profile: built
  Requires: artifact.build
  Evidence: invokes node dist/server.js
```

Both commands provide `application.run`, but they operate in different contexts.

If Suss cannot establish what `start` or `dev` does, it should report the command without assigning a profile.

### Command behavior (post-v0)

Suss may also record useful execution characteristics:

```text
long-running
modifies source files
writes build artifacts
requires network access
requires external services
```

These characteristics should be evidence-backed. They are particularly important if Suss later gains the ability to execute commands.

## Requirements and preparation

Requirements are environmental facts, not commands.

Suss should distinguish:

### Runtimes

```text
Node.js 22
Go 1.24
Python 3.13
Java 21
```

### Required tools

```text
pnpm 9
golangci-lint 2
Docker
Terraform
```

### Services

```text
PostgreSQL
Redis
Kafka
```

### Environment variables

```text
DATABASE_URL      required; no value discovered
REDIS_URL         required; default present
API_SECRET        supplied by a CI secret
```

Suss must never expose secret values. It should report only variable names, whether they appear required, and whether a non-secret default exists.

Preparation commands are separate from requirements:

```text
corepack enable
pnpm install --frozen-lockfile
go mod download
docker compose up -d postgres redis
```

Potential evidence sources include:

- runtime version files;
- manifest metadata;
- lockfiles;
- Docker Compose services;
- CI service containers;
- `.env.example`;
- devcontainer configuration;
- setup actions in CI.

Suss may eventually recommend ways to satisfy requirements, but it should distinguish discovered facts from environment-specific recommendations.

For example:

```text
Requirement:
  Node.js 22

Possible installation:
  mise use node@22

Status:
  recommendation, not repository-defined
```

## Detector architecture

Suss uses a provider model:

```text
NodeProvider
GoProvider
ElixirProvider
GitHubActionsProvider
SemaphoreProvider
MakeProvider
DockerComposeProvider
```

Post-v0 providers include Python, Ruby, Rust, JVM, and .NET.

Providers emit evidence-backed findings rather than directly producing a final plan.

For example:

```text
Finding type: command
Name: test
Command: pnpm test --run
Working directory: frontend
Source: .github/workflows/ci.yml
Context: CI
Confidence: explicit
Detector: github-actions
```

Another provider may emit:

```text
Finding type: command
Name: test
Command: pnpm test
Working directory: frontend
Source: frontend/package.json#scripts.test
Context: declared task
Confidence: explicit
Detector: node
```

The reconciliation layer connects these findings and determines that the CI command is a variant of the declared task.

The reconciliation layer is responsible for:

- grouping related findings;
- preserving command variants;
- attaching interpretations;
- ranking answers for a particular question;
- recording ambiguity and conflicts;
- producing the final project plans.

## Passive detection first

The core command should only inspect and detect:

```text
$ suss .
```

It should not:

- install dependencies;
- execute repository scripts;
- start services;
- run build tools;
- access secrets;
- access the network unnecessarily.

Command execution may be added later:

```text
$ suss run test
```

Detection and execution should remain separate library APIs:

```text
detect(path) → ProjectPlan
execute(plan, command) → Result
```

The `test` argument in `suss run test` would be a query for a command interpreted as providing `test.run`. It would not refer to a universal lifecycle stage.

Separating detection and execution keeps the library safe for inspecting unfamiliar repositories.

## Closest existing analogy

The closest existing model is buildpack detection.

Cloud Native Buildpacks inspect source files to determine which buildpacks participate in building an application. Paketo, for example, detects Node installation from files such as `package.json`.

[Paketo buildpack detection](https://paketo.io/docs/concepts/buildpacks/)

Nixpacks went further: its providers detect a language and generate setup, install, build, and start phases in a reusable plan.

[Nixpacks plan model](https://nixpacks.com/docs/how-it-works)

Suss is approximately:

> Nixpacks-style detection for common developer operations, without requiring containerization or imposing a universal lifecycle.

The distinction is that Suss also detects:

- tests;
- linting;
- formatting;
- type checking;
- local execution commands;
- CI-specific variants;
- runtime versions;
- required local services;
- environment variable names;
- repository-specific wrappers and task runners.

Suss treats existing repository configuration as evidence rather than replacing it with a newly generated build system.

## A sharp v0

The initial version should support:

- Node.js and TypeScript;
- Go;
- Elixir;
- GitHub Actions;
- Semaphore;
- Docker Compose;
- Make;
- package-manager scripts;
- common runtime-version files;
- common test, lint, type-check, and build tools.

Python, Ruby, and other ecosystems come after v0.

### Dogfood repositories and evaluation

v0 is working when it produces correct, evidence-backed plans for the repositories in our orbit:

- `superplanehq/superplane`
- `operately/operately`
- `semaphoreio/semaphore`

To avoid overfitting to our own conventions, the corpus also includes neutral open-source repositories of different shapes, ordered roughly from simple to complex:

- `chalk/chalk` — a trivial single-package npm library with plain scripts; the baseline case.
- `spf13/cobra` — a simple Go library with no task runner; exercises pure convention-based inference (`go test ./...`) plus golangci-lint and GitHub Actions.
- `elixir-ecto/ecto` — an Elixir library driven by Mix, whose CI matrix runs against database services.
- `caddyserver/caddy` — a Go application with a plain toolchain and a cross-platform GitHub Actions matrix.
- `excalidraw/excalidraw` — a TypeScript Yarn-workspaces monorepo with Vitest, where root scripts delegate into packages; exercises workspace detection.
- `plausible/analytics` — a polyglot Elixir/Phoenix application with a React/TypeScript frontend, a Makefile, and PostgreSQL + ClickHouse services; structurally close to the dogfood repositories but neutral.
- `grafana/grafana` — a very large Go + TypeScript monorepo with a heavy Makefile and extensive CI; the stress test.

Each corpus repository has an expected golden plan checked in, and detection runs against the corpus as snapshot tests. The corpus is the definition of done for v0 and the regression suite as providers evolve.

### Interpretations in v0

Detection is purely static and deterministic; no LLM is involved. Capabilities are assigned from a small, declarative knowledge base that maps well-known tool invocations (`vitest`, `jest`, `eslint`, `tsc`, `go test`, `golangci-lint`, `mix test`, ...) to capabilities. The knowledge base is data, not code, so it is testable and easy to extend.

If a command does not match the knowledge base, it is reported without interpretation. Execution profiles and command behavior characteristics are deferred past v0.

### Workspaces in v0

v0 detects project roots and per-project commands. Workspace orchestrators (`pnpm-workspace.yaml`, `turbo.json`, `nx.json`, `go.work`) are detected and reported as project facts, and root-level orchestrator commands are recorded with repository-wide scope. Fan-out from orchestrator commands to member packages is not modeled, and orchestrator commands are never attributed to individual packages.

### Implementation

- Suss is implemented in Go and distributed as a single static binary, so agents and CI can invoke it with no runtime dependencies.
- The versioned JSON output is the primary product. The human-readable CLI output is a renderer of the JSON, never a separate code path.
- A published JSON Schema describes the output, and the schema version appears in every plan.
- Command IDs are deterministic and stable across re-detections of the same repository, so consumers can reference commands persistently.

Initial commands:

```text
suss .
suss . --json
suss explain test
suss list
```

The initial library API should focus on detection:

```text
detect(path) → ProjectPlan[]
```

A compelling promise is:

> Run one command in an unfamiliar repository and learn what it requires and how it is configured to install dependencies, build, test, lint, type-check, and run locally—with every answer traced to supporting repository evidence.

This is useful as a standalone CLI and library. It also provides the detection foundation that a future verification planner could consume without making verification planning part of Suss itself.
