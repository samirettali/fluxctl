---
name: miniflux
description: Use fluxctl for Samir's Miniflux RSS reader — recap feeds, read/search articles, read/star/save entries, manage subscriptions and categories, import/export OPML, edit/import articles, clean history, mark an entire scope read and update enclosure playback. Use for RSS reading and user-facing Miniflux actions, not user/API-key/server administration.
compatibility: Requires fluxctl with MINIFLUX_URL and MINIFLUX_API_KEY set, or an unlocked rbw vault holding the miniflux-api-key entry.
---

# Miniflux

`fluxctl` reads and manages the current Miniflux account. stdout is JSON; errors are JSON on stderr. Commands follow Miniflux 2.3.0–2.3.3; `entry ids` requires 2.3.2+. No user CRUD, API-key management or server settings. The full field reference and versioned endpoint inventory are in `docs/user-api.md` in the fluxctl repository.

## Output shapes

The envelope is Miniflux's own: feeds and categories are arrays, entries are `{"total", "entries": [...]}`. Only the objects inside are trimmed:

```
category  {id, title, feed_count, total_unread}
feed      {id, title, site_url, feed_url, category:{id,title}, checked_at, disabled, parsing_error?}
entry     {id, title, url, author?, published_at, created_at, status, starred, reading_time,
           feed:{id,title}, category:{id,title}, enclosures?:[{url, mime_type}], tags?, content?}
```

`enclosures` is where a podcast episode or a YouTube video lives; `url` is the page. Object-returning commands support `--full`, often about ten times the size. Use it only when a needed field is absent (for example an enclosure ID or subscription settings).

**`--full` is not verbatim for secrets:** subscription passwords/cookies and credential-bearing feed integration URLs are always redacted, including nested feeds in entries. Proxy URLs lose credentials/query/fragment, and URL userinfo is removed elsewhere. Never try to recover these credentials from output. Feed credentials are supplied only through a private `--input` JSON file; no password/cookie/authenticated-proxy flags exist. Do not print that file or its contents.

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

- Only mutate when asked. A recap does not mark anything read; a "flag the important ones" request stars, it does not save, unless the user says so. Broad/destructive operations need an explicit matching request, not an inference from a recap.
- Report entries with title, feed and URL, never bare IDs. Keep the IDs at hand for the follow-up action.
- On a 404, re-check the ID. Never automatically retry a mutation after any error: the server may already have applied it, including when the response is lost or malformed.
- `feed list` shows `parsing_error` on feeds Miniflux cannot fetch; mention them only when the user asks about feed health.
- Never print API keys or subscription credentials. OPML can contain authenticated URLs; keep exports private and never print imported/exported contents.

## Subscriptions and categories

```sh
fluxctl feed discover --url https://example.com       # read-only; choose a returned feed URL
fluxctl feed create --feed-url https://example.com/rss --category 3
fluxctl feed update 7 --category 4 --title 'Research' --disabled=false
fluxctl feed update 7 --crawler=true
fluxctl feed create --input /private/subscription.json
fluxctl feed refresh 7
fluxctl feed refresh --all
fluxctl category create --title 'Research' --hide-globally
fluxctl category update 3 --hide-globally=false
fluxctl category refresh 3
fluxctl feed delete 7
fluxctl category delete 3
```

**Delete is destructive:** removing a feed removes its entries; removing a category removes its feeds and their entries. Refresh-all/category refresh queues work, so the acknowledgment does not prove all feeds have finished refreshing.

Flags work before or after IDs. Create/update/discovery support `--input FILE` JSON; flags override fields from that file. Omitted update fields stay unchanged, `--boolean=false` sends explicit false, and updates require at least one field. Feed JSON keys match the public API (e.g. `feed_url`, `category_id`, `crawler`, rules, `disabled`); corresponding nonsecret flags use hyphens (`--feed-url`, `--category`). Category input accepts `title`/`hide_globally`. Secret fields `username`, `password`, `cookie`, `proxy_url` are file-only; authenticated feed URLs also belong in that file, not shell arguments. Use `feed get ID --full` to inspect nonsecret configuration.

## Scope-wide read, editing and cleanup

```sh
fluxctl feed mark-all-read 7
fluxctl category mark-all-read 3
fluxctl entry mark-all-read                       # current account only; no user-ID argument
fluxctl entry update 42 --title 'Corrected' --content-file article.html
fluxctl entry import 7 --url https://example.com/article --content-file article.html --status unread
fluxctl entry import 7 --input article.json
fluxctl entry fetch-update 42                     # persist fetched content; unlike plain fetch
fluxctl entry flush-history
```

Mark-read changes the whole named scope. Feed/category operations use the server's current-time publication cutoff; future-published entries can remain unread. Account-wide mark-read uses only the ID returned by `/me`.

Update accepts `title`/`content` via flags or JSON; `--content-file` cannot be combined with `content`. Import accepts `url`, `title`, `content`, `author`, `comments_url`, `published_at` (positive Unix seconds), `status`, `starred`, `external_id`, and JSON-only `tags` (string array). Import defaults to **read** when status is omitted. Re-import can affect an existing matching entry: `starred=false` does not unstar it; use `unstar` separately. `fetch-update` explicitly persists the fetched article and changes `changed_at`; plain `fetch` is still read-only. Both return text, or HTML with `--html`.

**`flush-history` permanently deletes read, non-starred, non-shared entries and prevents re-ingestion with tombstones.** It is asynchronous; `accepted:true` means accepted, not finished. Do not substitute it for mark-read or assume it is reversible.

## OPML, playback and additional reads

```sh
fluxctl opml export --output subscriptions.opml
fluxctl opml import --input subscriptions.opml
fluxctl entry list --tag go --tag rss --globally-visible --starred=false
fluxctl entry list --before-id 500 --after-id 100
fluxctl entry ids --status unread --limit 1000 --offset 0
fluxctl entry get 42 --full                        # obtain enclosure IDs
fluxctl enclosure get 8
fluxctl enclosure update 8 --media-progression 120 # nonnegative playback seconds; 0 resets
fluxctl feed icon 7
fluxctl icon get 9
fluxctl integration status
fluxctl server version
fluxctl version                                  # local CLI version, no API call
```

OPML uses explicit regular input files and new output paths, not `-` or XML stdout. Export stages mode-0600 data in a private mode-0700 directory and atomically publishes by no-clobber hard link; it refuses existing or concurrently created files/symlinks. Hard-link support is required, with no overwrite fallback. Cleanup never removes the public output path. Receipts are JSON (`{output,bytes}` or `{action,accepted}`). Import changes subscriptions/categories, not articles, and a remote failure can leave partial effects. Do not automatically retry it. Sensitive OPML/subscription failures omit server response text while preserving status and authentication fixes.

`entry ids` requires 2.3.2+, returns `{total,entry_ids}`, defaults to all statuses and 1000 IDs in descending ID order, and accepts page sizes 1–10000. It supports status/starred/limit/offset, not general feed/category filters; use `entry list` for those. `--starred=false` explicitly selects unstarred entries; omission leaves starred state unfiltered. Visibility false removes that filter; it does not select only hidden entries. Icons are JSON with MIME type/base64 data, not implicit binary downloads.
