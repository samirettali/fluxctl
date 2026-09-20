# fluxctl

Agent-friendly CLI for [Miniflux](https://miniflux.app): read articles, manage subscriptions and categories, import/export OPML, and act on your own account. stdout is JSON, errors are JSON on stderr, and default objects are trimmed. No user/API-key/server administration.

Supports Miniflux 2.3.0–2.3.3 contracts; `entry ids` needs 2.3.2+. See the [endpoint coverage matrix and command reference](docs/user-api.md) for versioned sources, all fields and intentional exclusions.

## Install

```sh
go build -o fluxctl . && install -m755 fluxctl ~/.local/bin/
```

## Configure

Create an API key in Miniflux under Settings > API Keys, then either export it:

```sh
export MINIFLUX_URL=https://rss.example.com
export MINIFLUX_API_KEY=...
```

or store it in an [rbw](https://github.com/doy/rbw) entry named `miniflux-api-key`, with the key as the password and the instance URL as the URI. fluxctl reads the vault when the environment is missing and the vault is unlocked.

## Usage

```sh
fluxctl me
fluxctl category list
fluxctl feed list [--category ID]
fluxctl feed get ID
fluxctl feed counters
fluxctl entry list [--status unread|read|all] [--starred] [--feed ID] [--category ID]
                   [--since 24h|7d|2w|RFC3339] [--until ...] [--published-since ...] [--published-until ...]
                   [--search QUERY] [--limit N] [--offset N] [--order FIELD] [--direction asc|desc] [--content]
fluxctl entry get ID [--html]
fluxctl entry fetch ID [--html]
fluxctl entry read ID...
fluxctl entry unread ID...
fluxctl entry star ID...
fluxctl entry unstar ID...
fluxctl entry save ID...
fluxctl feed discover --url URL
fluxctl feed create --feed-url URL [--category ID] [--input FILE]
fluxctl feed update ID [--title TITLE] [--category ID] [--disabled=false] [--input FILE]
fluxctl feed delete ID
fluxctl feed refresh ID | --all
fluxctl category create --title TITLE [--hide-globally]
fluxctl category update ID [--title TITLE] [--hide-globally=false]
fluxctl category delete ID
fluxctl category refresh ID
fluxctl feed mark-all-read ID
fluxctl category mark-all-read ID
fluxctl entry mark-all-read
fluxctl entry update ID [--title TITLE] [--content-file FILE]
fluxctl entry import FEED_ID --url URL [--content-file FILE] [--status read|unread]
fluxctl entry fetch-update ID [--html]
fluxctl entry flush-history
fluxctl entry ids [--status read|unread|all] [--starred=false] [--limit N] [--offset N]
fluxctl enclosure get ID
fluxctl enclosure update ID --media-progression SECONDS
fluxctl feed icon ID
fluxctl icon get ID
fluxctl opml import --input FILE
fluxctl opml export --output NEW_FILE
fluxctl integration status
fluxctl server version
fluxctl version
```

Object-returning commands take `--full` to retain Miniflux's fields instead of trimming. **Exception: subscription secrets are always redacted, even with `--full` and in nested feed objects.** Passwords/cookies and credential-bearing integration URLs are hidden; proxy URLs lose userinfo/query/fragment, and URL userinfo is removed elsewhere. `entry list` omits content unless `--content` is passed; `entry get` and `entry fetch` return it as plain text, or as HTML with `--html`. `--since`/`--until` filter on when the entry last changed (arrival time for unread entries), `--published-since`/`--published-until` on the publication date. `--limit 0` returns everything; the server caps a page at 1000.

Flags work before or after IDs. Only supplied update fields are sent; use `--flag=false` to explicitly clear a boolean. Feed credentials (`username`, `password`, `cookie`, `proxy_url`) are accepted **only through `--input` JSON files**, not command-line flags. Keep those files private. Every supported field is listed in the [command reference](docs/user-api.md).

**Mutations are explicit and do not prompt or retry.** Deleting a feed/category also deletes its entries/subscriptions. `entry flush-history` asynchronously and permanently removes read, non-starred, non-shared articles. `entry mark-all-read` affects only the current account; it accepts no user ID. Plain `entry fetch` stays read-only; `fetch-update` explicitly persists content. Imports can have partial effects if the server fails; inspect before retrying.

OPML uses explicit file paths and JSON receipts, never XML stdout. Export stages mode-0600 data in a private directory, then atomically publishes without overwriting files or symlinks. The destination filesystem must support hard links; unsupported filesystems fail closed. Cleanup never removes the public destination. OPML can contain subscription credentials; its contents and sensitive API error bodies are never printed.

`entry list` also accepts repeated `--tag`, `--globally-visible`, `--before-id`, `--after-id` and `--starred=false`. `server version` queries Miniflux; `version` remains local.

The canonical agent skill lives in `.agents/skills/miniflux`.
