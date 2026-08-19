# Suss

Suss inspects a repository and reports how a developer or agent should set it up, build it, test it, lint it, and run it. Every answer is traced to structured evidence in the repository.

Suss is under active development. See [plan.md](plan.md) for current status and [idea.md](idea.md) for the design.

## Supported

- Languages and frameworks: JavaScript, TypeScript, Go, Rust, Elixir, Phoenix, Ruby, Rails, PHP, Laravel, Symfony, Python, Django, Flask, and FastAPI.
- Package and task tooling: npm, pnpm, Yarn, Bun, Cargo, Mix, Bundler, Rake, Composer, pip, Poetry, uv, Pipenv, PDM, and Make.
- Repository automation: GitHub Actions, Semaphore, and Docker Compose.
- Structured signals: manifests, lockfiles, runtime-version files, tool configuration, CI services, and environment-variable names.
- Output: a default human-readable plan or the versioned JSON document.

## Requirements

Go 1.26 or later (see `go.mod`).

## Build

```text
go build -o suss ./cmd/suss
```

That writes a `suss` binary in the repository root. `go install ./cmd/suss` installs it to `$(go env GOPATH)/bin`.

## Hello world

Shallow-clone Chalk and inspect it without installing its dependencies or running any repository commands:

```sh
git clone --depth 1 https://github.com/chalk/chalk.git chalk
suss chalk
```

The default human output looks like this:

```text
chalk
=====

  How to work with this project:
    Purpose                 Command
    ----------------------  -------
    Install dependencies    npm install
    Test, Lint, Type-check  npm test

  Project details:
    Languages: javascript, typescript
    Package managers: npm
    Requirements:
      runtime node >=22
```

## Run

```text
suss .
suss . --uninterpreted --evidence
suss . --json
suss path/to/repo
```

Without `--json`, Suss prints a human-readable plan. Uninterpreted commands and evidence are omitted unless requested with `--uninterpreted` and `--evidence`. With `--json`, it emits the versioned plan document. Detection is static: it does not install dependencies or execute repository commands.

## Test

`make check` runs the full local gate (format, lint, race tests, module tidiness, vulnerability scan) and is what CI enforces.

```text
make check
```

Corpus snapshots live under `testdata/golden/`. Remote corpus repositories are shallow-fetched into `testdata/cache/` on first run.

Licensed under the Apache License, Version 2.0.
