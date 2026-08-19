# Suss

Suss scans a repository and explains how to build, test, lint, and run it.

Suss only reads repository files. It does not install dependencies or run repository commands.

## What Suss detects

- Languages and frameworks: JavaScript, TypeScript, Go, Rust, Elixir, Phoenix, Ruby, Rails, PHP, Laravel, Symfony, Python, Django, Flask, and FastAPI
- Package managers and task tools: npm, pnpm, Yarn, Bun, Cargo, Mix, Bundler, Rake, Composer, pip, Poetry, uv, Pipenv, PDM, and Make
- Automation and services: GitHub Actions, Semaphore, and Docker Compose
- Project information from manifests, lockfiles, runtime version files, tool configuration, CI services, and environment variable names

Suss can print a readable text plan or a versioned JSON document.

## Requirements

You need Go 1.26 or later. See `go.mod`.

## Build

```text
go build -o suss ./cmd/suss
```

This creates a `suss` binary in the repository root.

To install the binary in `$(go env GOPATH)/bin`, run:

```text
go install ./cmd/suss
```

## Quick start

Clone the Chalk repository and scan it:

```sh
git clone --depth 1 https://github.com/chalk/chalk.git chalk
suss chalk
```

Suss prints output like this:

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

## Usage

```text
suss .
suss . --uninterpreted --evidence
suss . --json
suss path/to/repo
```

By default, Suss prints a human-readable plan.

### Flags

- Use `--uninterpreted` to print all commands that Suss found but could not reliably explain.
- Use `--evidence` to print the source files that support the results.
- Use `--json` to print the versioned JSON document.

## Development

Run the same checks as CI:

```text
make check
```

This command checks formatting, lint errors, race conditions, module files, and known vulnerabilities.

Expected test corpus output is stored in `testdata/golden/`. On the first test run, Suss clones remote test corpus repositories into `testdata/cache/`.

## License

Suss is licensed under the Apache 2.0 license.
