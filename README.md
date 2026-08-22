# marvelcdb-cli

A command-line client for [MarvelCDB](https://marvelcdb.com), the Marvel Champions card database.

Look up cards, packs, factions, FAQ entries and decklists without an account. Sign in with OAuth2 to read and edit your own decks. Credentials live in your operating system's keyring, and responses are cached on disk so repeated queries are instant.

```console
$ marvelcdb cards list --faction leadership --type ally --max-cost 2
CODE   NAME                          TYPE  FACTION     COST  PACK
01067  Maria Hill                    Ally  Leadership  2     Core Set
03013  Squirrel Girl (Doreen Green)  Ally  Leadership  2     Captain America
03014  Wonder Man (Simon Williams)   Ally  Leadership  2     Captain America
...
```

## Install

```bash
go install github.com/mggarofalo/marvelcdb-cli/cmd/marvelcdb@latest
```

Or build from a clone:

```bash
make build      # produces ./marvelcdb
make install    # installs into $GOPATH/bin
```

Pre-built binaries for macOS, Linux and Windows are attached to each [release](https://github.com/mggarofalo/marvelcdb-cli/releases).

## Getting started

```bash
marvelcdb cache warm                  # download the card list once, up front
marvelcdb cards search "web-warrior"
marvelcdb cards get 01001a -o text
marvelcdb packs list
marvelcdb decklists popular
```

Shell completion is built in:

```bash
marvelcdb completion zsh > "${fpath[1]}/_marvelcdb"    # zsh
marvelcdb completion bash > /etc/bash_completion.d/marvelcdb
```

Completions for `--pack`, `--faction`, `--type`, `--set` and card codes are filled from the cached card data, so run `marvelcdb cache warm` first and they will be instant.

## Commands

| Command | What it does |
|---|---|
| `cards list` | List cards with filters: pack, type, faction, set, trait, cost, text |
| `cards search <query>` | Search names, text, traits and flavour |
| `cards get <code>...` | Full details for one or more cards |
| `cards image <code>...` | Print card image URLs |
| `packs list` / `packs get` | Products, with release dates and completeness |
| `factions list` | Aspects and the pseudo-factions MarvelCDB uses |
| `faq <code>...` | Rules clarifications for a card |
| `taboos list` | Banned and restricted list (see [known upstream issues](#known-upstream-issues)) |
| `decklists get <id>...` | A published decklist |
| `decklists by-date <date>` | Everything published on one day (`today`, `yesterday`, or `YYYY-MM-DD`) |
| `decklists popular` | What MarvelCDB is featuring |
| `decks get <id>...` | A shared but unpublished deck |
| `mydecks ...` | Read and edit your own decks (needs sign-in) |
| `auth ...` | Manage credentials |
| `cache ...` | Inspect, warm, prune and clear the HTTP cache |
| `config ...` | Read and write settings |

Every command accepts `--output`/`-o` with `table` (default), `json`, `yaml`, `csv`, `tsv`, `ids` or `text`.

`json` output is **verbatim**: the models round-trip fields this build has never heard of, so `marvelcdb cards get 01001a -o json` reproduces exactly what MarvelCDB sent, and stays correct when the site adds a field.

`ids` prints the first column only, which makes piping easy:

```bash
marvelcdb cards list --set spider_man -o ids | xargs marvelcdb cards image
```

## Signing in

The deck-management endpoints are OAuth2-protected, and **MarvelCDB issues client credentials to registered applications only**. You request them by email from the site's maintainers (see <https://marvelcdb.com/api/>), telling them the redirect URI your client will use. The default this CLI listens on is `http://127.0.0.1:8765/callback`; MarvelCDB matches redirect URIs exactly, so ask for that one unless you have a reason not to.

Once you have an id and secret:

```bash
marvelcdb auth client set --id <client-id>     # prompts for the secret
marvelcdb auth login                           # opens a browser, captures the callback
marvelcdb auth status
```

If your registered redirect URI is not a loopback address:

```bash
marvelcdb auth login --manual                  # prints the URL, reads the code back
```

Then:

```bash
marvelcdb mydecks list
marvelcdb mydecks new --hero 01001a --name "Webhead" --aspect justice
marvelcdb mydecks get 12345 -o text --with-cards
```

### Where credentials are stored

The client secret and the access and refresh tokens go into the OS keyring — Keychain on macOS, Secret Service on Linux, Credential Manager on Windows. **Nothing secret is ever written to the config file.**

On a machine with no keyring (CI, a container, a headless box), set these instead:

| Variable | Purpose |
|---|---|
| `MARVELCDB_CLIENT_ID` | OAuth2 client id |
| `MARVELCDB_CLIENT_SECRET` | OAuth2 client secret |
| `MARVELCDB_ACCESS_TOKEN` | Access token |
| `MARVELCDB_REFRESH_TOKEN` | Refresh token |

The environment takes precedence over the keyring.

### Editing a deck

MarvelCDB has no "add one card" operation. A save sends the complete slot list and replaces what was stored, so the safe pattern is read, edit, write back:

```bash
marvelcdb mydecks get 12345 -o json > deck.json
# edit the "slots" object
marvelcdb mydecks save 12345 --slots-file deck.json --problem too_few_cards
```

For small edits, `--slot CODE=QUANTITY` is repeatable and layers on top of `--slots-file`. A quantity of `0` removes a card:

```bash
marvelcdb mydecks save 12345 --slots-file deck.json --slot 01050=2 --slot 01053=0
```

`--dry-run` shows what would be sent without sending it. `mydecks delete` and `mydecks publish` confirm before acting; `--yes` skips the prompt.

## Caching

MarvelCDB serves the whole card corpus as a single ~7 MB document, and it has no server-side query language. This CLI therefore downloads the corpus once, filters locally, and caches on disk. Once an entry goes stale it is revalidated with `If-None-Match` / `If-Modified-Since`, so an unchanged corpus costs a `304` rather than another download.

| Flag | Effect |
|---|---|
| `--refresh` | Revalidate even fresh entries |
| `--no-cache` | Neither read nor write the cache |
| `--offline` | Serve only from cache; fail if a response is missing (exit 5) |
| `--cache-ttl 24h` | How long an entry stays fresh before revalidating |

```bash
marvelcdb cache info      # where it lives, how big it is
marvelcdb cache warm      # pre-fetch cards, packs and factions
marvelcdb cache prune --older-than 30d
marvelcdb cache clear
```

Authenticated responses are never cached: a shared on-disk cache is the wrong home for per-user data.

Requests are also spaced (100 ms apart by default, `--rate-limit`) and identified with a descriptive `User-Agent`. MarvelCDB is a community-run site; the defaults are deliberately courteous.

## Configuration

Settings live in a YAML file. Precedence, lowest to highest: built-in defaults → config file → environment → flags.

```bash
marvelcdb config path
marvelcdb config list
marvelcdb config set output json
marvelcdb config set cache_ttl 24h
```

| Setting | Default | Notes |
|---|---|---|
| `base_url` | `https://marvelcdb.com` | Override for a mirror or a test server |
| `locale` | `en` | `de`, `es`, `fr`, `it`, `ko` — served from language subdomains |
| `output` | `table` | Default output format |
| `color` | `auto` | `auto`, `always`, `never`; `NO_COLOR` forces `never` |
| `cache_dir` | OS cache dir | Where responses are stored |
| `cache_ttl` | `1h` | Freshness window before revalidating |
| `rate_limit` | `100ms` | Minimum gap between requests |
| `timeout` | `60s` | Per-request timeout, retries included |
| `max_retries` | `4` | Total attempts; `1` disables retrying |
| `redirect_uri` | `http://127.0.0.1:8765/callback` | Must match what MarvelCDB registered |
| `keyring_service` | `marvelcdb-cli` | Keyring service name |

Corresponding environment variables: `MARVELCDB_CONFIG`, `MARVELCDB_BASE_URL`, `MARVELCDB_LOCALE`, `MARVELCDB_OUTPUT`, `MARVELCDB_CACHE_DIR`, `MARVELCDB_CACHE_TTL`, `MARVELCDB_USER_AGENT`.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | General failure |
| 3 | Not signed in, or credentials rejected |
| 4 | Not found |
| 5 | `--offline` and no cached response |
| 130 | Interrupted |

## Known upstream issues

These are MarvelCDB's, not this CLI's. They are surfaced honestly rather than papered over.

- **Unknown ids return HTTP 500, not 404.** `cards get`, `faq`, `decklists get` and `decks get` translate that specific response into "not found" and exit 4. Because of this, `500` is never retried — retrying could not succeed and would only add load.
- **`taboos list` fails server-side.** The endpoint is routed but currently returns HTTP 500 for everyone. The command exists for when it is fixed.
- **`mydecks publish --precedent` fails.** MarvelCDB's handler dereferences an undefined variable on that path. The flag is accepted because the API documents it; leave it unset.
- **Only JSON is served.** The API routes advertise `xml`, `xls` and `xlsx` for the per-pack card endpoint, but the handler rejects everything except JSON, so the CLI does not offer them.

## API coverage

Every documented MarvelCDB endpoint is implemented. See [docs/api-coverage.md](docs/api-coverage.md) for the endpoint-by-endpoint mapping.

## Development

See [AGENTS.md](AGENTS.md).

```bash
make test        # go test ./... -race
make test-cover  # with a coverage report
make lint        # golangci-lint
make fmt         # gofumpt
```

## License

MIT. Not affiliated with MarvelCDB, Fantasy Flight Games or Marvel.
