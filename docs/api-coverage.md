# MarvelCDB API coverage

Every endpoint MarvelCDB routes is implemented. This table maps each one to the
CLI command and the Go client method that calls it.

Endpoint paths were read from
[`routing_api_public.yml`](https://github.com/zzorba/marvelsdb/blob/master/src/AppBundle/Resources/config/routing/api/routing_api_public.yml)
and
[`routing_api_oauth2.yml`](https://github.com/zzorba/marvelsdb/blob/master/src/AppBundle/Resources/config/routing/api/routing_api_oauth2.yml)
in the MarvelCDB source, then verified against the live site.

## Public endpoints

No authentication. Cached on disk and revalidated with conditional requests.

| Method | Path | Command | Client method |
|---|---|---|---|
| GET | `/api/public/packs/` | `packs list`, `packs get` | `Client.Packs` |
| GET | `/api/public/factions/` | `factions list` | `Client.Factions` |
| GET | `/api/public/cards/` | `cards list`, `cards search` | `Client.Cards` |
| GET | `/api/public/cards/{pack_code}.json` | `cards list --pack <code>` | `Client.CardsByPack` |
| GET | `/api/public/card/{card_code}.json` | `cards get`, `cards image` | `Client.Card` |
| GET | `/api/public/faq/{card_code}.json` | `faq` | `Client.Faq` |
| GET | `/api/public/taboos/` | `taboos list` | `Client.Taboos` |
| GET | `/api/public/decklist/{id}.json` | `decklists get` | `Client.Decklist` |
| GET | `/api/public/decklists/by_date/{date}.json` | `decklists by-date` | `Client.DecklistsByDate` |
| GET | `/api/public/decklists/popular/` | `decklists popular` | `Client.PopularDecklists` |
| GET | `/api/public/deck/{id}.json` | `decks get` | `Client.PublicDeck` |

### Query parameters

- `?encounter=1` on `/api/public/cards/` adds encounter-deck and villain cards.
  Exposed as `cards list --encounter`. It roughly doubles the response, from
  about 2,150 cards to about 4,450.
- `?jsonp=<callback>` wraps the response for cross-domain browser use. Not
  exposed: a CLI has no use for it, and it would make the output invalid JSON.

### Deliberately not exposed

**`GET /api/public/user/info`.** Routed, but it authenticates from a session
cookie, and `/api/public` sits outside MarvelCDB's OAuth2 firewall. An API
client always gets `null` back. A command that can only ever print `null` would
be worse than none.

**`.xml`, `.xls` and `.xlsx` on the per-pack card endpoint.** The route
advertises them; the handler answers `xlsx format not supported. Only json is
supported.` with HTTP 200 and an HTML content type. Nothing to expose.

## OAuth2 endpoints

Behind MarvelCDB's `^/api/oauth2` firewall. The bearer token is attached only to
these paths, so public reads stay anonymous and cacheable. These responses are
never written to the disk cache.

| Method | Path | Command | Client method |
|---|---|---|---|
| GET | `/api/oauth2/decks` | `mydecks list` | `Client.MyDecks` |
| GET | `/api/oauth2/deck/load/{id}` | `mydecks get` | `Client.LoadDeck` |
| POST | `/api/oauth2/deck/new` | `mydecks new` | `Client.NewDeck` |
| PUT | `/api/oauth2/deck/save/{id}` | `mydecks save` | `Client.SaveDeck` |
| DELETE | `/api/oauth2/deck/delete/{id}` | `mydecks delete` | `Client.DeleteDeck` |
| PUT | `/api/oauth2/deck/publish/{id}` | `mydecks publish` | `Client.PublishDeck` |

Write parameters are sent as `application/x-www-form-urlencoded`, including on
`PUT`. Symfony populates its request bag from a form-encoded `PUT` body, which
is what the handlers read.

## OAuth2 flow

| Purpose | Path |
|---|---|
| Authorize | `/oauth/v2/auth` |
| Token | `/oauth/v2/token` |

`marvelcdb auth login` runs the authorization-code grant. It generates a random
`state`, starts a one-shot HTTP server on the loopback address in the redirect
URI, opens a browser, verifies the returned `state`, and exchanges the code.
`--manual` prints the URL and reads the code (or the whole redirected URL) back
from the terminal instead, for clients registered with a non-loopback redirect.

MarvelCDB runs FOSOAuthServerBundle, which does not support PKCE, so the client
secret is required and is stored in the OS keyring.

## Response quirks worth knowing

**Unknown ids return HTTP 500.** Not 404 — a `{"error":{"code":500,...}}`
envelope. The client treats that response as "not found" on the single-record
endpoints only (`card`, `faq`, `decklist`, `deck`, and the OAuth2 deck routes),
where an id is being looked up. On collection endpoints a 500 stays a real
server error. This is also why 500 is never retried: retrying could not
succeed, and would only add load to a community-run site.

**Writes fail with HTTP 200.** The OAuth2 write endpoints answer refusals with
`{"success":false,"msg":"..."}` and a 200 status. The client checks the envelope
and turns a false `success` into an error carrying the server's message.

**`linked_card` carries explicit nulls.** A nested `linked_card` object includes
`"attack": null` for stats the card does not have, while a top-level card omits
the key entirely. The models round-trip both forms, so `-o json` reproduces the
response byte for byte.

**Decklists include `"tags": ""`.** Empty strings are sent, not omitted. Same
mechanism preserves them.

**`/decklists/popular/` leaks a Doctrine proxy.** Each entry carries a `faction`
object of `{"__initializer__":{},"__cloner__":{},"__isInitialized__":false}`.
It is preserved verbatim in JSON output and ignored everywhere else.

## Translations

MarvelCDB serves translations from language subdomains, not from a query
parameter. `--locale de` rewrites `marvelcdb.com` to `de.marvelcdb.com`.
Confirmed working: `de`, `es`, `fr`, `it`, `ko`. `en` uses the bare host.
