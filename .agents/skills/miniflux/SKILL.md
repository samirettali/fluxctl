---
name: miniflux
description: Read Samir's Miniflux RSS reader with fluxctl — list what came in recently, per category or feed, read an article's text, search entries, then mark entries read, star them, or save them to linkding. Use when asked for a recap of the news, what is new in the feeds, what is worth reading or watching, to catch up on a topic or a source, or to act on articles (read, star, save).
compatibility: Requires fluxctl with MINIFLUX_URL and MINIFLUX_API_KEY set, or an unlocked rbw vault holding the miniflux-api-key entry.
---

# Miniflux

`fluxctl` reads the Miniflux instance and acts on entries. stdout is JSON; errors are JSON on stderr.

## Output shapes

The envelope is Miniflux's own: feeds and categories are arrays, entries are `{"total", "entries": [...]}`. Only the objects inside are trimmed:

```
category  {id, title, feed_count, total_unread}
feed      {id, title, site_url, feed_url, category:{id,title}, checked_at, disabled, parsing_error?}
entry     {id, title, url, author?, published_at, created_at, status, starred, reading_time,
           feed:{id,title}, category:{id,title}, enclosures?:[{url, mime_type}], tags?, content?}
```

`enclosures` is where a podcast episode or a YouTube video lives; `url` is the page. `--full` on `category list`, `feed list|get` and `entry list|get` returns Miniflux's objects verbatim, about ten times the size. Do not use it unless the trimmed object lacks a field the task needs.

## Authentication

**Never check configuration before running a command.** Anything that needs it fails with the fix in the error:

```json
{"error": "miniflux is not configured", "fix": "export MINIFLUX_URL and MINIFLUX_API_KEY, or ...", "details": "rbw is locked: run 'rbw unlock'"}
```

Relay the `fix` to the user; unlocking the vault cannot be done for them.

## Recap of what came in

Start from the categories to know the landscape, then list entries for the window asked for:

```sh
fluxctl category list
fluxctl entry list --since 24h --limit 0           # every unread entry fetched in the last day
fluxctl entry list --since 7d --category 9 --limit 0
fluxctl entry list --since 2d --feed 703
```

`--since`/`--until` take `24h`, `7d`, `2w` or an RFC 3339 timestamp and filter on when the entry last changed. For unread entries that is when Miniflux fetched them, which is what "since yesterday" means and is safe against feeds with bogus publication dates; with `--status all` an old entry that was just read or starred also matches. `--published-since`/`--published-until` filter on the publication date instead. `entry list` is unread-only by default, ordered by `published_at desc`, 50 per page; `--order created_at` sorts by arrival, `--limit 0` returns everything in one response with no cap (an explicit `--limit` may be at most 1000), `--status all` includes read entries, `--starred` keeps only starred ones.

`total` is the count for the whole filter, not the page. With several hundred entries, group by `category.title` and `feed.title` and summarize per group rather than listing each item.

For a recap, titles and feeds are usually enough to pick what matters. Read content only for the items to be highlighted:

```sh
fluxctl entry get 140084                # plain text content
fluxctl entry get 140084 --html         # original HTML
fluxctl entry fetch 140084              # ask Miniflux to download the full page, for summary-only feeds
fluxctl entry list --since 24h --category 9 --limit 0 --content   # content of every entry, costly
```

`entry fetch` answers `{"id", "content", "reading_time"}` and fails with a 500 when the page is gone; fall back to the URL in the entry. It does not update the entry in Miniflux: a later `entry get` still returns the summary.

## Search

```sh
fluxctl entry list --search "account abstraction" --status all --limit 20
fluxctl entry list --search kafka --since 30d
```

Search is Miniflux full-text search over title and content.

## Acting on entries

```sh
fluxctl entry read 140084 140085        # one request for all IDs
fluxctl entry unread 140084
fluxctl entry star 140084 140085        # idempotent: already-starred entries are skipped
fluxctl entry unstar 140084
fluxctl entry save 140084               # send to the integration configured in Miniflux (linkding)
```

`star`/`unstar` answer `{"starred", "changed", "skipped", "failed": [{"id", "error"}]}`, `save` answers `{"saved", "failed"}`; both go on past a per-entry failure, so report `failed` whenever it is non-empty. `saved` means Miniflux accepted the entry and is sending it in the background: a failure on the linkding side never comes back, so confirm with the linkding skill when it matters. `read`/`unread` answer `{"status", "entries"}`, which echoes the input: Miniflux accepts unknown IDs silently, so a typo is not caught there. Take IDs from a previous `entry list` or `entry get`, never from memory.

## Operating rules

- Only mutate when asked. A recap does not mark anything read; a "flag the important ones" request stars, it does not save, unless the user says so.
- Report entries with title, feed and URL, never bare IDs. Keep the IDs at hand for the follow-up action.
- On a 404, re-check the ID before retrying.
- `feed list` shows `parsing_error` on feeds Miniflux cannot fetch; mention them only when the user asks about feed health.
- Never print the API key.
