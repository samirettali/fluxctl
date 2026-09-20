# User API coverage

## Version contract and sources

The baseline is **Miniflux 2.3.0**; the inventory also covers **2.3.3**, the latest
upstream release inspected. `entry ids` requires **2.3.2+**. Other commands use
contracts present in 2.3.0 and unchanged in 2.3.3. This is source- and mock-tested
compatibility, not a claim of live-instance integration testing. Older versions
may work but are not the supported contract. No version probe, retry, mutation
fallback, installation or server configuration change is performed automatically.
An unsupported route returns the server's normal JSON error.

Inventory sources (all paths below are relative to `/v1`):

- [2.3.0 routes](https://github.com/miniflux/v2/blob/2.3.0/internal/api/api.go)
  (`c2944df4398433caa0a1d75c628141f369334156`).
- [2.3.3 routes](https://github.com/miniflux/v2/blob/2.3.3/internal/api/api.go)
  (`a4ba097af79e451eac6aa42ac144fe47f75cf9f0`).
- [2.3.1 routes](https://github.com/miniflux/v2/blob/2.3.1/internal/api/api.go) versus
  [2.3.2 routes](https://github.com/miniflux/v2/blob/2.3.2/internal/api/api.go):
  entry ID listing and bulk starred state were added in 2.3.2.
- Request contracts: [feed](https://github.com/miniflux/v2/blob/2.3.3/internal/model/feed.go),
  [category](https://github.com/miniflux/v2/blob/2.3.3/internal/model/category.go),
  [entry](https://github.com/miniflux/v2/blob/2.3.3/internal/model/entry.go),
  [enclosure](https://github.com/miniflux/v2/blob/2.3.3/internal/model/enclosure.go),
  [discovery](https://github.com/miniflux/v2/blob/2.3.3/internal/model/subscription.go),
  [API messages/import](https://github.com/miniflux/v2/blob/2.3.3/internal/api/messages.go).
- Semantics: [entry handlers](https://github.com/miniflux/v2/blob/2.3.3/internal/api/entry_handlers.go),
  [entry storage](https://github.com/miniflux/v2/blob/2.3.3/internal/storage/entry.go),
  [OPML handlers](https://github.com/miniflux/v2/blob/2.3.3/internal/api/opml_handlers.go),
  [current-user mark-read guard](https://github.com/miniflux/v2/blob/2.3.3/internal/api/user_handlers.go).

## Endpoint / capability matrix

| Public endpoint | CLI / disposition |
| --- | --- |
| `GET /me` | `me`; also resolves the identity for account-wide mark-read |
| `GET /categories` | `category list` (includes counts) |
| `POST /categories` | `category create` |
| `PUT /categories/{id}` | `category update ID` |
| `DELETE /categories/{id}` | `category delete ID` (also removes its feeds and entries) |
| `PUT /categories/{id}/refresh` | `category refresh ID` |
| `PUT /categories/{id}/mark-all-as-read` | `category mark-all-read ID` |
| `GET /categories/{id}/feeds` | `feed list --category ID` |
| `GET /categories/{id}/entries` | Existing `entry list --category ID`; no redundant command |
| `GET /categories/{id}/entries/{entry}` | Existing `entry get ENTRY`; no redundant scoped lookup |
| `POST /discover` | `feed discover` (does not subscribe) |
| `GET /feeds` | `feed list` |
| `GET /feeds/{id}` | `feed get ID` |
| `GET /feeds/counters` | `feed counters` |
| `POST /feeds` | `feed create` |
| `PUT /feeds/{id}` | `feed update ID` (including move/category and fetch settings) |
| `DELETE /feeds/{id}` | `feed delete ID` (also removes its entries) |
| `PUT /feeds/{id}/refresh` | `feed refresh ID` |
| `PUT /feeds/refresh` | `feed refresh --all` |
| `PUT /feeds/{id}/mark-all-as-read` | `feed mark-all-read ID` |
| `GET /feeds/{id}/icon` | `feed icon ID` |
| `GET /feeds/{id}/entries` | Existing `entry list --feed ID`; no redundant command |
| `GET /feeds/{id}/entries/{entry}` | Existing `entry get ENTRY`; no redundant scoped lookup |
| `POST /feeds/{id}/entries/import` | `entry import FEED_ID` |
| `GET /entries` | `entry list`, including category/feed, status/starred, tags, visibility, time, ID cursors, search, order and pagination |
| `GET /entries/ids` | `entry ids` (**2.3.2+**), paginated IDs in descending ID order |
| `GET /entries/{id}` | `entry get ID` |
| `PUT /entries` | `entry read ID...` / `entry unread ID...`; bulk starred field (2.3.2+) intentionally not duplicated: existing `star`/`unstar` also work on 2.3.0 |
| `PUT /entries/{id}` | `entry update ID` (title/content) |
| `PUT /entries/{id}/bookmark`, `/star` | `entry star ID...` / `entry unstar ID...`; one alias is enough; GET-before-toggle preserves idempotent intent |
| `POST /entries/{id}/save` | `entry save ID...` |
| `GET /entries/{id}/fetch-content` | `entry fetch ID` (read-only); `entry fetch-update ID` explicitly sends `update_content=true` |
| `PUT /users/{id}/mark-all-as-read` | `entry mark-all-read`: GET `/me`, then PUT using **only that returned ID**; no user-ID argument |
| `PUT /flush-history`, `DELETE /flush-history` | `entry flush-history` uses DELETE; no duplicate alias |
| `GET /export` | `opml export --output NEW_FILE` |
| `POST /import` | `opml import --input FILE` |
| `GET /icons/{id}` | `icon get ID` |
| `GET /enclosures/{id}` | `enclosure get ID` |
| `PUT /enclosures/{id}` | `enclosure update ID --media-progression SECONDS` |
| `GET /integrations/status` | `integration status` (whether a save integration is enabled) |
| `GET /version` | `server version`; `version` remains the local CLI version |
| `POST /users`, `GET /users`, `GET /users/{identifier}`, `PUT /users/{id}`, `DELETE /users/{id}` | **Excluded:** user CRUD, lookup of other accounts and account settings |
| `POST /api-keys`, `GET /api-keys`, `DELETE /api-keys/{id}` | **Excluded:** API-key administration |

Server settings, integration configuration, account preferences, sharing controls,
individual entry deletion and enclosure media downloads have no in-scope public
API operation here. No private web-UI routes are called. In particular, `removed`
is not a supported entry status in these versions; history cleanup is the public
API's destructive article cleanup operation. Icons already return JSON containing
MIME type and base64 data, so no implicit binary download/file write is necessary.

## CLI request and output conventions

Flags can go before or after IDs. Every ID must be positive. Mutations operate on
one explicitly named object, except existing bulk entry verbs and explicit
`--all`/account-wide verbs. There is no prompt, implicit confirmation, automatic
retry or hidden follow-up mutation. Read-only discovery, fetch and export do not
change subscriptions/articles. A remote mutation may already have completed if
its response is lost or malformed: inspect server state before retrying.

Commands returning no API body print an acknowledgment such as:

```json
{"action":"feed delete","accepted":true,"id":7}
```

This means the HTTP request succeeded, not that an asynchronous job finished.
Feed/category refresh-all jobs and history flushing can still be running; save
integration failures do not come back to the CLI. Single-feed refresh runs through
the server's normal refresh handler. Account mark-read stops on an identity lookup
failure without issuing a PUT. All commands fail immediately on authentication
errors, preserving the existing `fix` remedy.

Object-returning new commands support `--full`; default feed/article responses
reuse the existing trimmed objects. `entry update` returns plain-text content by
default; `--full` retains HTML. Category writes return `{id,title,hide_globally}`;
create-feed returns `{feed_id}`, article import `{id}`. Icons return
`{id,mime_type,data}`, enclosures `{id,entry_id,url,mime_type,size,media_progression}`.
Use `entry get ID --full` to obtain enclosure IDs from a feed article. There is no
implicit request to download the enclosure URL.

**Security exception to `--full`: subscription secrets are always redacted**, on
all routes, including feed objects nested inside articles. Nonempty password and
cookie fields become `[redacted]`; proxy URLs lose userinfo, query and fragment;
URL userinfo is removed elsewhere. Feed integration URLs (`webhook_url` and
`apprise_service_urls`) are redacted because their paths may be credentials.
Known secret values are also scrubbed from response text such as parsing errors.
Nonsecret fields, including subscription usernames, remain usable. This is not a
general-purpose scrubber for arbitrary secrets embedded in article prose or
custom rule strings: do not put credentials in those fields.

Subscription creation/update/discovery and OPML error bodies are suppressed rather
than echoed, because a server may return credentials or an entire document in its
error. Status codes and authentication remedies remain. Input parsing errors do
not quote file contents. No command logs request payloads.

### Input files, omission and explicit false

Create/update/discovery/import commands take a JSON object via `--input FILE`.
Only the documented fields for that operation are accepted; null, unknown fields
and incorrect types are rejected. Flags override the same field from the file.
An omitted update field is not sent. For example:

```sh
fluxctl feed update 7 --disabled=false --crawler=true
fluxctl category update 3 --hide-globally=false
fluxctl enclosure update 8 --media-progression 0
```

Boolean flags require `=false`, not a separate `false` positional. Empty strings
clear rule/credential fields supported by upstream; empty title, content, site URL,
feed URL and description updates are rejected because upstream rejects/ignores
them. Updates require at least one field.

`username`, `password`, `cookie` and `proxy_url` are accepted **only in feed JSON
input files**, never as command-line flags. Keep these files private; the CLI does
not impose a new ingress permission requirement. Do not commit, print or include
real credentials in examples. An authenticated subscription URL also belongs in
the file, not shell arguments. Use real paths, not stdin/stdout conventions.

### Feeds

```sh
fluxctl feed discover --url https://example.com
fluxctl feed create --feed-url https://example.com/rss --category 3
fluxctl feed update 7 --category 4 --title 'New title'
fluxctl feed create --input /private/subscription.json
fluxctl feed refresh 7
fluxctl feed refresh --all
fluxctl feed delete 7
```

Supported fields (flags replace `_` with `-`, except `category_id` → `--category`):

| Operations | JSON fields |
| --- | --- |
| Discover | `url`, `user_agent`, `username`, `password`, `cookie`, `proxy_url`, `fetch_via_proxy`, `allow_self_signed_certificates`, `disable_http2` |
| Create/update strings | `feed_url`, `user_agent`, `scraper_rules`, `rewrite_rules`, `blocklist_rules`, `keeplist_rules`, `block_filter_entry_rules`, `keep_filter_entry_rules`, `urlrewrite_rules` |
| Create/update file-only credentials | `username`, `password`, `cookie`, `proxy_url` |
| Create/update booleans | `crawler`, `ignore_entry_updates`, `disabled`, `no_media_player`, `ignore_http_cache`, `allow_self_signed_certificates`, `fetch_via_proxy`, `hide_globally`, `disable_http2` |
| Create/update integer | `category_id` (positive when supplied; create omission uses the server's default category) |
| Update-only strings | `site_url`, `title`, `description` |

These are the public API's subscription fields, not arbitrary response fields.
TLS/proxy/crawler changes only happen when explicitly requested. Ordinary remote
fetching is done by Miniflux, not by the CLI.

### Categories and bulk read

```sh
fluxctl category create --title 'Research' --hide-globally
fluxctl category update 3 --title 'Reading' --hide-globally=false
fluxctl category refresh 3
fluxctl feed mark-all-read 7
fluxctl category mark-all-read 3
fluxctl entry mark-all-read
fluxctl category delete 3
```

Category JSON input accepts `title` and `hide_globally`. **Deleting a category also
deletes its subscriptions and entries**; deleting a feed also deletes its entries.
Feed/category mark-read use the server's current-time cutoff (future-published
articles can remain unread); account mark-read marks all unread entries for the
current account. No command accepts another user's ID.

### Article edit, import, fetch-update and history

```sh
fluxctl entry update 42 --title 'Corrected title' --content-file article.html
fluxctl entry import 7 --url https://example.com/article --content-file article.html --status unread
fluxctl entry import 7 --input article.json
fluxctl entry fetch-update 42
fluxctl entry flush-history
```

Update JSON accepts `title` and `content`. Import JSON accepts `url` (required),
`title`, `content`, `author`, `comments_url`, `published_at` (positive Unix seconds),
`status` (`read` or `unread`), `starred`, `external_id`, and `tags` (string array,
file-only). Corresponding flags are available except `tags`. `--content-file`
loads article content and cannot be combined with a `content` field/flag. Both
text and HTML are accepted by the server; upstream sanitizes HTML.

Import defaults to **read** when status is omitted. Re-import matches
`external_id` (or URL when absent) and can update an existing entry's status;
`starred=true` stars it, but false does **not** unstar an existing entry. Use
`entry unstar` explicitly for that. Import cannot restore a tombstoned entry.

`fetch-update` persists the fetched content and changes `changed_at`; plain
`fetch` still does neither. Both return `{id,content,reading_time}` with text by
default or HTML with `--html`. Because the upstream mutation uses GET, the CLI
uses an isolated non-reusing transport to prevent automatic transport replay and
rejects redirects that drop `update_content=true`.

**`flush-history` permanently removes read, non-starred, non-shared entries** and
records tombstones to prevent re-ingestion. It is asynchronous (202 accepted),
not a preview or a mark-read operation. There is no automatic polling or retry.

### Additional reads

```sh
fluxctl entry list --tag go --tag rss --globally-visible --starred=false
fluxctl entry list --before-id 500 --after-id 100
fluxctl entry ids --status unread --limit 1000 --offset 0
fluxctl enclosure get 8
fluxctl enclosure update 8 --media-progression 120
fluxctl feed icon 7
fluxctl icon get 9
fluxctl integration status
fluxctl server version
fluxctl version
```

`entry ids` (2.3.2+) returns `{total,entry_ids}`, sorted by ID descending; default
status is all, limit 1000, offset 0, and page size must be 1–10000. It intentionally
does not accept general entry-list filters the endpoint does not implement.
`--starred=false` is an explicit filter on both ID and article listing; omission
means no starred-state filter. Visibility false disables that filter; it does not
select only hidden feeds. Tags use repeated `--tag` flags and upstream matching.
Enclosure progression is nonnegative seconds, including zero to reset playback.

### OPML files

```sh
fluxctl opml export --output subscriptions.opml
fluxctl opml import --input subscriptions.opml
```

Export writes a **mode-0600 file inside a private mode-0700 temporary directory**
in the output directory. Only after XML validation, writing and closing succeed
is that complete file published by an **atomic no-clobber hard link**. Existing or
concurrently created files, directories and symlinks are never overwritten. The
public output path is never reserved or unlinked by cleanup, including after a
successful publication. Missing parent directories are not created.

This requires hard-link support on the destination filesystem (for example APFS
on macOS or ext4 on Linux); unsupported filesystems fail with a clear error and
**no overwrite/copy fallback**. Staging beside the destination avoids cross-device
links. Failure cleanup removes only the private staging file/directory, never the
public destination; if the OS refuses staging cleanup, a private staging artifact
may remain. This protects against concurrent writers to the output pathname, not
arbitrary hostile code with the same UID or replacement of trusted parent directories.
The successful receipt is `{output,bytes}`. Exported OPML may contain authenticated
URLs: keep the file private. Its contents are never printed.

Import reads a regular file, checks that it is one well-formed OPML document, and
POSTs the XML with `Content-Type: application/xml`. XML directives/DTDs are not
supported. It imports subscriptions/categories, not article archives; remote
partial effects are possible if the server fails midway. The receipt is
`{"action":"opml import","accepted":true}`; neither OPML nor a server message
that might echo OPML goes to stdout/stderr. `-` is not stdin/stdout: explicit local
paths are required. No overwrite/force flag is provided.
