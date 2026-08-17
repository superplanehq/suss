# Suss

Suss inspects a repository and reports how a developer or agent should set it up, build it, test it, lint it, and run it. Every answer is traced to structured evidence in the repository.

Suss is under active development. See [plan.md](plan.md) for current status and [idea.md](idea.md) for the design.

## Requirements

Go 1.26 or later (see `go.mod`).

## Build

```text
go build -o suss ./cmd/suss
```

That writes a `suss` binary in the repository root. `go install ./cmd/suss` installs it to `$(go env GOPATH)/bin`.

## Run

```text
./suss .
./suss . --json
./suss path/to/repo
```

Without `--json`, Suss prints a human-readable plan. With `--json`, it emits the versioned plan document. Detection is static: it does not install dependencies or execute repository commands.

## Test

```text
go test ./...
```

CI also runs `gofmt` and `go vet`. Corpus snapshots live under `testdata/golden/`. Remote corpus repositories are shallow-fetched into `testdata/cache/` on first run.

```text
gofmt -l .
go vet ./...
```

Licensed under the Apache License, Version 2.0.
