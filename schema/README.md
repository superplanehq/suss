# Suss plan contract v1

This directory is the reviewable contract for Suss's machine-readable output.
`plan.v1.schema.json` is authoritative for serialized JSON; `../plan/types.go`
is the matching Go representation.

## Document shape

Every invocation returns one document:

- `schemaVersion` is the string `"1"`.
- `projects` contains one plan per detected project root.

Repository-local paths use forward slashes. `"."` means the inspected
repository root. Absolute paths are never emitted.

Every collection is required and is encoded as an array, including when empty.
Optional scalar fields are omitted rather than encoded as `null`. The sole
exception is `Command.run`, where `null` explicitly means that a declared task
exists but its invocation is unresolved. Unknown object fields are rejected.

## Project plans

A project plan contains:

- `path`: the project root relative to the inspected repository.
- `languages`: evidence-backed language names.
- `frameworks`: evidence-backed framework names.
- `packageManagers`: every detected package manager; keeping this plural lets
  conflicting lockfiles remain visible.
- `facts`: extensible namespaced facts, such as
  `workspace.orchestrator = pnpm`.
- `requirements`: runtimes, tools, services, and environment variables.
- `preparation`: exact setup commands, kept separate from normal commands.
- `commands`: exact commands the project exposes, CI uses, or Suss infers.
- `ambiguities`: plausible alternatives for which no selection can be made.
- `conflicts`: contradictory assertions, optionally with a justified
  resolution.

The contract intentionally does not model workspace fan-out, execution
profiles, command side effects, or secret values.

`packageManagers` is an evidence inventory, not a selected answer. When
multiple entries disagree, a `tool.package-manager` conflict resolution is the
authoritative selection. If there is no resolution, consumers must treat the
package manager as unresolved.

## Evidence

Evidence has a `kind`, `source`, and optional logical `pointer` and
`description`.

Kinds have these meanings:

- `file`: file presence is the evidence.
- `declaration`: a structured repository declaration is the evidence.
- `invocation`: structured automation invokes the value.
- `configuration`: tool configuration establishes the value.
- `convention`: a named ecosystem rule produced the value.

For file-backed evidence, `source` is a repository-relative path. For a
convention it is a stable convention identifier. `pointer` identifies a
logical field or step and must not use a line number, because line numbers make
references unstable under unrelated edits.

Every conclusion, command, candidate, and resolution carries at least one
piece of evidence.

## Confidence and command origin

Confidence is deliberately separate from provenance:

- `high`: direct, unambiguous structured evidence or strong corroboration.
- `medium`: indirect or incomplete evidence that still supports one answer.
- `low`: a plausible conclusion with material uncertainty.

A command's `origin` says how its invocation was obtained:

- `declared`: a repository-owned task or wrapper exposes the command.
- `observed`: CI or other structured automation invokes it, but it has not
  been linked to a declaration.
- `inferred`: a named ecosystem convention generated it.

This avoids using `explicit` as both a source category and a confidence level.
An inferred Go command can, for example, still have high confidence when the
convention's prerequisites are all present.

## Commands and variants

`run` preserves the invocation exactly. It is `null` only when Suss has direct
evidence that a declared task exists but cannot determine how to invoke it.
Such a command remains in `commands`, and an ambiguity with the same
`commandId` carries the candidate invocations. When `run` is `null`, the
command's confidence describes confidence that the task exists; it does not
rank the candidate invocations. `directory` is repository-relative. `scope` is
`project` or `repository`.

Interpretations use only the initial capability vocabulary:

- `dependencies.install`
- `artifact.build`
- `test.run`
- `code.lint`
- `code.format`
- `code.typecheck`
- `application.run`

Interpretations are optional and never replace native command names. A command
that cannot be classified has an empty `interpretations` array.

A contextual invocation linked to a primary command is stored in `variants`.
The initial v0 context is `ci`. A CI invocation that cannot be linked remains
an independent command with `origin: "observed"` instead of being guessed into
a variant relationship.

## Deterministic command IDs

Command IDs are source identities, not hashes of command text. Changing a
script body therefore does not invalidate references to the task.

The ID input is the UTF-8 byte sequence:

```text
suss.command.v1 NUL projectPath NUL provider NUL source NUL pointer
```

The output is `cmd_` followed by the lowercase hexadecimal encoding of the
first 16 bytes of the SHA-256 digest.

- `projectPath` is the plan path, including `"."` for the root.
- `provider` is a stable lowercase kebab-case provider name.
- `source` is a repository-relative source path or stable convention name.
- `pointer` is the provider's stable identity inside that source, such as
  `/scripts/test` or `test`.

If one structured location contains several shell commands, the provider adds
a stable sub-command suffix such as `#command=0` to the pointer. Providers must
not include display names, line numbers, confidence, interpretations, or `run`
text in the identity.

The 128-bit digest makes IDs unique across project plans while keeping snapshot
output manageable.

## Requirements and secret safety

Requirements use one of four kinds:

- `runtime`
- `tool`
- `service`
- `environment`

Version is optional because repositories often establish presence without
pinning a version. Environment requirements must include `isRequired` and
`hasDefault`, and must not include a version or any value. Suss never stores or
emits secret or default values.

## Ambiguities and conflicts

An ambiguity means several candidates are plausible and Suss cannot select
one. Each candidate contains a machine-readable `value`, optional explanatory
text, and its own evidence. A declared task always remains in `commands`; when
its invocation is ambiguous, `run` is `null` rather than an arbitrary candidate
and the ambiguity's `commandId` links back to it. Undeclared candidate commands
are not promoted into `preparation` or `commands` merely to make the output
look decisive.

A conflict means structured evidence makes contradictory assertions. An
optional `resolution` records the selected value, reason, confidence, and
evidence. Without a defensible resolution, the field is omitted. `commandId`
links a conflict to the command it qualifies when applicable.

`subject` is a stable namespaced key such as `dependencies.install`,
`tool.package-manager`, or `runtime.node.version`.

## Deterministic ordering

Producers sort every array before emit so golden snapshots are byte-stable.
`plan.Document.Sort` is the implementation of these rules:

1. projects by `path`;
2. languages and frameworks by `name`;
3. package managers by `name`, then `version`;
4. facts by `name`, then `value`;
5. requirements by `kind` (`runtime`, `tool`, `service`, `environment`), then
   `name`, then `version`;
6. preparation and commands by `id`;
7. interpretations by `capability`;
8. variants by `context`, then `run`, then `directory`;
9. evidence by kind precedence (`declaration`, `invocation`, `configuration`,
   `file`, `convention`), then `source`, then `pointer`, then `description`;
10. ambiguities and conflicts by `subject`, then `commandId` (absent before
    present), then `message`;
11. candidates and assertions by `value`.

Unknown enum values sort after the known order, then lexicographically.
Ordering is a producer invariant rather than a JSON Schema constraint. The
JSON encoder does not escape HTML characters, so command text is stored
exactly, and documents are indented with two spaces and a trailing newline.

## Draft compatibility

Version 1 is still a milestone-1 draft. Until the milestone-7 schema freeze,
examples and types may change together without compatibility guarantees. The
freeze must define whether adding enum members is compatible or requires a new
schema version; the current strict schema intentionally does not decide that
policy by accident.

## Review checkpoint

Before phase 1b, confirm:

- the strict required-array and no-unknown-fields policy;
- a unified `requirements` array rather than four category arrays;
- plural `packageManagers`;
- high/medium/low confidence with a separate command-origin enum;
- source-derived opaque command IDs;
- nullable `run` only for declared tasks with linked command ambiguities;
- generic candidate values for ambiguity/conflict records;
- preparation commands using the same `Command` shape as normal commands.
