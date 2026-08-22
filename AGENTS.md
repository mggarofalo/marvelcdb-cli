# AGENTS.md

This file provides guidance to AI agents working with code in this repository.

## Project Overview

**marvelcdb-cli** is a Go command-line client for [MarvelCDB](https://marvelcdb.com), the Marvel Champions card database.

- Public endpoints (cards, packs, factions, FAQ, decklists) need no account
- OAuth2 endpoints (deck management) need client credentials MarvelCDB issues by email
- Credentials live in the **OS keyring**, never in the config file
- Responses are cached on disk and revalidated with conditional requests

The binary is `marvelcdb`; the module is `github.com/mggarofalo/marvelcdb-cli`.

## Prerequisites

| Tool | Version | Notes |
|---|---|---|
| Go | 1.26+ | See `go.mod` |
| golangci-lint | v2.11+ | `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.0` |
| gofumpt | latest | `make fmt` |

## Quick Start

```bash
make build        # build ./marvelcdb
make test         # go test ./... -race
make test-cover   # with coverage.out and coverage.html
make lint         # golangci-lint run
make vet          # go vet ./...
make fmt          # gofumpt -w .
```

## Project Structure

```
cmd/marvelcdb/       # entrypoint; ldflags inject version and commit
internal/
  api/               # MarvelCDB HTTP client, models, retry and rate limiting
  auth/              # OS keyring storage and the OAuth2 authorization-code flow
  cache/             # on-disk HTTP cache with conditional revalidation
  cli/               # cobra command tree
  config/            # YAML settings, environment overrides
  output/            # table / json / yaml / csv / tsv / ids / text renderers
  version/           # build identity
docs/api-coverage.md # endpoint-by-endpoint mapping, and upstream quirks
```

| Package | Responsibility |
|---|---|
| `api` | One method per MarvelCDB endpoint. Knows nothing about cobra or terminals. |
| `auth` | `Store` (keyring + environment) and `Login` (authorization-code flow). |
| `cache` | An `http.RoundTripper` that stores responses and revalidates them. |
| `cli` | Flag parsing, filtering, rendering. `Factory` builds collaborators on demand. |
| `config` | Defaults, file, environment. Flags are applied on top in `Factory.resolve`. |
| `output` | Everything that decides how a result looks. |

## Design decisions worth preserving

**JSON output is verbatim.** Every model round-trips fields it does not know
about, plus explicit nulls and empty strings, through an `Extra` map. That is
why `-o json` reproduces MarvelCDB's response byte for byte, and why it keeps
working when the site adds a field. `internal/api/extras.go` does the work;
`TestModelsRoundTripVerbatim` guards it. **Do not add a field to a model without
checking that test still passes.**

**HTTP 500 means "not found" on single-record endpoints.** MarvelCDB answers an
unknown card code with a 500 error envelope. `request.singleRecord` marks the
endpoints where that translation is correct; collection endpoints keep 500 as a
real error. This is also why the retry transport never retries 500.

**Writes can fail with HTTP 200.** The OAuth2 write endpoints signal refusal in
`{"success":false,"msg":"..."}`. `checkMutation` turns that into an error.

**The bearer token goes only to `/api/oauth2/`.** Public reads stay anonymous so
they stay cacheable, and the cache refuses to store anything carrying an
`Authorization` header.

**Filtering is local.** MarvelCDB has no query language, so `cards list` fetches
the corpus and filters in `internal/cli/cardfilter.go`. The cache is what makes
this acceptable.

**Traits are separated by `". "`, not `"."`.** Splitting on `"."` shreds
`S.H.I.E.L.D.` into single letters. See `Card.TraitList` and `normaliseTrait`.

**Nothing secret reaches disk.** Config holds preferences only. If you add a
setting, ask whether it is a credential; if it is, it belongs in `auth`.

## Testing

- Table-driven tests throughout
- `internal/cli` drives the whole command tree against a fake MarvelCDB
  (`newFakeMarvelCDB` in `command_test.go`), which also pins `MARVELCDB_BASE_URL`
  so **no test can reach the live site**
- `internal/auth` exercises the full OAuth2 loopback flow against `httptest`
- Credentials in tests come from `MemoryBackend` or the environment, never the
  real keyring
- Run `make test` (race detector on) before committing

When adding a command, add an end-to-end case to `TestPublicCommandsEndToEnd` or
`TestMyDecksEndToEnd` and a handler to the fake server.

## Coding Standards

- `internal/` only; no `pkg/` directory
- Wrap errors with context: `fmt.Errorf("read config %s: %w", path, err)`
- Sentinel errors are matched with `errors.Is`; `api.ErrNotFound` and
  `api.ErrUnauthorized` drive the exit codes in `cli.exitCode`
- Comments explain *why*, not *what*; upstream quirks get a comment saying so
- Every exported identifier has a doc comment
- Help text is prose a user can act on, not a restatement of the flag name
- Advisory messages go to stderr via `printer.Warn`, so piped stdout stays clean

## Adding an endpoint

1. Add the method to `internal/api/public.go` or `oauth2.go`, with a `request`
   value; set `singleRecord: true` if it addresses one record by id
2. Add or extend a model in `internal/api/models.go`, keeping the `Extra` field
   and its `MarshalJSON`/`UnmarshalJSON` pair
3. Add the command in `internal/cli/`, wired in `NewRootCommand`
4. Add a handler to the fake server and an end-to-end test case
5. Update `docs/api-coverage.md` and the command table in `README.md`

## Commit Convention

[Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <description>

Types: feat, fix, refactor, test, docs, chore, ci, build
Scopes: api, auth, cache, cli, config, output, docs, ci
```

Examples:
- `feat(cli): add cards image command`
- `fix(api): treat HTTP 500 on card lookup as not found`
- `docs: document the taboos endpoint outage`

## Branching

Feature branches off `main`, squash-merged through a PR. `main` is protected:
PR required, CI must pass, no force-push.

Releases are plain annotated git tags, `v0.1.0` onwards, following [semantic
versioning](https://semver.org). There is no release automation: tag a commit on
`main` and push the tag.

```bash
git tag -a v0.2.0 -m "v0.2.0"
git push origin v0.2.0
```

Users install by building from source, so a tag is all that is needed — `go
install ...@v0.2.0` and `make install` both resolve it. `.github/workflows/release.yml`
will additionally build and attach binaries for macOS, Linux and Windows *if*
you publish a GitHub Release from a tag; it does nothing on a bare tag push.

## Agent Rules

- Run `make test` and `make lint` before committing
- Never commit credentials, tokens, or a populated config file
- Do not weaken the "no test reaches the live site" guarantee
- When MarvelCDB behaves oddly, document the quirk in `docs/api-coverage.md`
  and the README's "Known upstream issues" rather than hiding it
