# CLAUDE.md
 
This file provides guidance to Claude Code when working with code in this repository.
 
## Repository
 
`github.com/remnestal/albstractions` — a collection of lightweight, general-purpose Go utility modules.
 
## Structure

- The root directory contains no Go code — only submodules (each with their own `go.mod` and independent semver lifecycle).
- Each submodule is an independent Go module under `github.com/remnestal/albstractions/<name>`.
 
## Environment

This is a private GitHub repo under `github.com/remnestal/albstractions`.

## Commands

### Task runner

A root `Taskfile.yml` runs lint/test/fmt/tidy across every module (modules are
discovered dynamically, so new ones need no Taskfile edits).

```bash
task verify        # lint, test (-race), and require fmt/tidy clean — the commit gate
task fmt           # apply gofmt across every module
task tidy          # apply go mod tidy across every module
task hooks:install # one-time per clone: enable the versioned pre-commit hook
```

`task hooks:install` points `core.hooksPath` at `.githooks/`, whose `pre-commit`
hook runs `task verify`. Bypass for WIP with `git commit --no-verify`.

### Raw commands (no task runner)

```bash
# Run all tests across all modules
find . -name go.mod | xargs -I{} dirname {} | xargs -I{} sh -c 'cd {} && go test ./...'
 
# Run tests for a single module
cd keyloader && go test ./...
 
# Run a single test
cd keyloader && go test ./path/to/pkg -run TestFunctionName
 
# Run a single subtest
cd keyloader && go test ./path/to/pkg -run TestFunctionName/subtest_name
 
# Run tests with race detector (always do this before tagging)
cd keyloader && go test -race ./...
 
# Lint a module
cd keyloader && golangci-lint run ./...
 
# Lint all modules
find . -name go.mod | xargs -I{} dirname {} | xargs -I{} sh -c 'cd {} && golangci-lint run ./...'
```

## Adding a New Module
 
1. Create a new directory at the repo root: `mkdir <module>`
2. Initialise the module: `cd <module> && go mod init github.com/remnestal/albstractions/<module>`
3. Follow the package layout conventions below.
4. Add CI path filtering for the new module (see Release & Tagging).
5. Add a new entry to `.github/dependabot.yml` with `directory: /<module>`.
6. Add the module to the `matrix.module` list in `.github/workflows/ci.yml`.
7. Create `<module>/README.md` with the module's purpose, install command, and a minimal usage example.
8. Add the module to the modules table in the root `README.md`.
9. Start versioning from `v0.1.0`.

## Inter-Module Dependencies
 
Some modules depend on others (e.g. `mtls` → `pki` → `keyloader`). When working locally:
 
- Use `replace` directives in `go.mod` to point at local paths during development.
- **Remove all `replace` directives before tagging a release** — they must not appear in published modules.
- Keep the dependency graph acyclic. If a circular dependency is tempting, it is a sign the boundary is wrong.

## Package Layout
 
- Each module's root package exposes the primary API.
- Each module ships a `README.md` at its root: purpose, install command, and a minimal usage example.
- `<module>/mock/` sub-packages provide test doubles intended for import in *other* projects — use these when the real implementation is too heavy for unit tests.
- Do not put shared helpers in the root of the repo — if something is needed by multiple modules, it either belongs in its own module or indicates the modules should be merged.

## Release & Tagging
 
Modules are tagged independently using the format `<module>/vX.Y.Z` (e.g. `keyloader/v1.2.0`). Go's toolchain resolves these natively.
 
**Tagging checklist before a release:**
1. All `replace` directives removed from `go.mod`.
2. `go mod tidy` has been run.
3. `go test -race ./...` passes.
4. `golangci-lint run ./...` is clean.
5. Tag format: `git tag <module>/vX.Y.Z && git push origin <module>/vX.Y.Z

Follow semver strictly:
- **Patch** (`v1.0.x`): bug fixes, no API change.
- **Minor** (`v1.x.0`): new backwards-compatible functionality.
- **Major** (`vX.0.0`): breaking API change. For v2+, the module path must include the major version suffix (e.g. `github.com/remnestal/albstractions/keyloader/v2`).
 
Do **not** retag an existing version. If something was wrong, release a new patch.

## Design Principles
 
**API shape:**
- Mandatory arguments belong in function signatures, not option structs (structs provide no type safety for zero values).
- Optional configuration uses the `(opts ...Option)` variadic pattern.
 
**Security and robustness are first-class** alongside simplicity and ease of use.
 
**Module independence:** each module's API must be complete and well-designed on its own merits — no module should expose a half-baked API just to power another module in this repo. Composition between `albstractions` modules is perfectly fine and encouraged when there is a clear fit — this is how the ecosystem grows coherently. Keep the dependency graph acyclic; a circular dependency is a sign the boundary is wrong.

## Comments & Godoc

- Every exported symbol must have a doc comment.
- Comments start with the symbol name: `// Retry executes f up to n times...`
- Doc comments begin with a single high-level summary line.
- Additional detail goes in one or more note blocks below the summary, each separated by an empty comment line (`//`). Example:
  ```go
  // Retry executes f up to n times, returning the last error.
  //
  // Between attempts it waits according to the configured backoff.
  // A zero n is treated as a single attempt.
  //
  // Retry is safe for concurrent use.
  func Retry(n int, f func() error) error { ... }
  ```
- `Panics if …`, `By default …`, and `Default: …` caveats are their own note block, never appended to the summary line.
- Document the *why* or non-obvious behaviour, not what the signature already says.
- Cross-references to other exported symbols use godoc `[Symbol]` doc-link syntax.
- In prose, use ASCII `<=` / `>=` / `< 0`, not `≤`/`≥`.
- Do not use decorative banner/separator comments (`// ----`); rely on godoc structure and file organisation.
- Package-level comments should describe purpose and typical usage in 2–3 lines; no need for a full example unless the API is non-obvious.
- Unexported symbols: comment only when the logic is non-trivial.

## Commit Messages
 
Use semantic commit messages with a module scope: `<type>(<module>): <description>`.

**Examples:**
- `fix(pki): handle expired root CA gracefully`
- `docs(mtls): clarify mTLS handshake example`

Common types: `feat`, `fix`, `chore`, `refactor`, `test`, `docs`.

## Testing Conventions
 
- Use `testify/assert` (non-fatal) and `testify/require` (fatal on failure).
- Each subtest lives in its own `t.Run(...)` block with a concise descriptive name explaining the expected behaviour.
- All tests must be parallelisable — call `t.Parallel()` at the top of every test and subtest.
- Use table-driven tests when multiple cases share the same structure but differ only in inputs/outputs.
