# fluxctl

Agent-friendly CLI for [Miniflux](https://miniflux.app). stdout is JSON, errors are JSON on stderr, and the default output is trimmed to what an agent needs to read a feed and act on it.

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
```

`category list`, `feed list`, `feed get`, `entry list` and `entry get` take `--full` to get Miniflux's own objects instead of the trimmed ones. `entry list` omits content unless `--content` is passed; `entry get` and `entry fetch` return it as plain text, or as HTML with `--html`. `--since`/`--until` filter on when the entry last changed (arrival time for unread entries), `--published-since`/`--published-until` on the publication date. `--limit 0` returns everything; the server caps a page at 1000.

The agent skill lives in `.agents/skills/miniflux`.
