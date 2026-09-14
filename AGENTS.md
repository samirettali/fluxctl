# AGENTS.md

`fluxctl` is an agent-friendly CLI for a Miniflux instance, written in Go. It follows the shape of
`spotctl`: JSON on stdout, JSON errors on stderr, trimmed objects by default and `--full` for the
raw ones, standard library only.

## Commands

- `go test ./...` — run tests.
- `go vet ./...` — run static checks.
- `gofmt -l .` — must print nothing.
- `go build -o fluxctl . && install -m755 fluxctl ~/.local/bin/` — how it is installed today.

## Status

Private repository, hand-installed into `~/.local/bin`. When it goes public it gets the spotctl
release path (GitHub release, NUR package, installed through dotfiles) and the skill is picked up
by `coding-agent-skills.nix` in dotfiles the way `spotify` is. Until then the skill is only
available from this checkout.

Scope for now is reading plus `read|unread|star|unstar|save`. Feed and category management
(create, move, delete, refresh, OPML) is deliberately left for later.

## Conventions

- **The envelope is Miniflux's; only the objects inside are trimmed.** Feeds and categories are
  bare arrays, entries are `{"total", "entries"}`. A trimmed entry keeps id, title, url, author,
  timestamps, status, starred, reading time, and `feed`/`category` as `{id, title}`; the raw one
  embeds the whole feed object and the HTML content, roughly ten times the bytes.
- **Content is opt-in and plain text by default.** `entry list --content`, `entry get` and
  `entry fetch` flatten the HTML with `htmlToText` (a regexp stripper, not a parser: it only has
  to leave the prose readable). `--html` keeps the markup. Go's regexp is RE2 and has no
  backreferences, so the script/style dropper lists each tag explicitly, and self-closing
  `<svg/>` is removed first because a lazy `<svg.*?</svg>` would otherwise start there and eat
  everything up to the next real closing tag. Source whitespace is flattened before tags are
  turned into breaks, so `<pre>` loses its layout; paragraphs become blank lines, list items
  and rows single breaks, table cells tabs.
- Trimmed entries keep `enclosures` as `{url, mime_type}` when present: for a podcast or a
  YouTube feed the entry URL is the page and the enclosure is the media.
- `entry fetch` answers `{"id", "content", "reading_time"}` in both text and `--html` mode. It
  does not send `update_content=true`, so the fetched page is returned but not stored and
  `changed_at` stays put; a later `entry get` still returns the feed's summary.
- **`--since`/`--until` map to `changed_after`/`changed_before`, not to Miniflux's
  `after`/`before`.** Measured on 2.3.0: `after`/`before` filter on `published_at` (an entry
  published in 1970 and fetched in April is excluded by `after=1`), and there is no
  `created_at` filter at all. `changed_at` is set at fetch time and moves again when the entry
  is read or starred, so on the unread default it is arrival time, which is what "since
  yesterday" means; on `--status all` a recently read old entry also matches, and the skill
  says so. `--published-since`/`--published-until` expose `published_after`/`published_before`
  for when the publication date is the point. All four accept `24h`, `7d`, `2w` or RFC 3339.
- `entry list` defaults to `--status unread`, `--order published_at --direction desc`,
  `--limit 50`. `--status all` drops the filter. **`limit` is always sent**: Miniflux defaults
  to 100 when it is absent and treats an explicit `0` as no cap, so `--limit 0` is "everything"
  only because the parameter is on the wire, and it really is everything in one response (no
  server-side page cap on 0). Above 1000 Miniflux answers 400, so that cap is enforced locally. `removed` is not a valid status filter on 2.3.0 (400), so it is not offered.
- `--order` accepts everything the server does (`id`, `status`, `published_at`, `created_at`,
  `changed_at`, `category_title`, `category_id`, `title`, `author`); `created_at` is the natural
  order for an arrival recap.
- **Configuration comes from `MINIFLUX_URL` and `MINIFLUX_API_KEY`, with the rbw entry
  `miniflux-api-key` as the fallback** (password = key, first URI = base URL). The fallback is
  what makes a bare binary in `~/.local/bin` work from an agent today; the dotfiles wrapper will
  set the variables later and the fallback goes idle. It never prompts: a locked vault is an
  error whose `fix` says to unlock it, since pinentry has no terminal from an agent.
- **Errors carry their own remedy.** Missing configuration and a 401 both render as
  `{"error", "fix", "details"}`, so no caller needs a status check first. `authError` wraps the
  `APIError`, so `errors.As` still reaches the status code.
- **`star`/`unstar` are idempotent on top of a toggle.** Miniflux only exposes
  `PUT /entries/{id}/bookmark`, which flips the flag, so each entry is read first and only
  flipped when it is not already in the requested state. The answer is
  `{"starred", "changed", "skipped", "failed"}`; one failure does not abort the rest, except an
  `authError`, which would fail every remaining entry the same way and is returned as is so the
  caller gets the `fix` on stderr and a non-zero exit. `save` follows the same rule.
- `read`/`unread` are one request for all IDs (`PUT /entries` takes a list). **Miniflux updates
  with `id = ANY(...)` and never checks the row count**, so an unknown ID is accepted and the
  answer just echoes the input; verifying each ID first would cost a GET per entry for a typo
  the skill tells agents to avoid. `star` does read first because the toggle needs the state.
  `save` is one
  request per entry (`POST /entries/{id}/save`), which sends it to the third-party integration
  configured in Miniflux — linkding on Samir's instance. **Miniflux answers 202 and dispatches
  in a goroutine**, so `saved` means accepted; a failure on the integration side never surfaces.
- Flags may come before or after positional arguments (`parseFlags` re-parses after each
  positional), because agents write `entry get 42 --full` as often as the other way round.
- A 2xx with an empty body (204 on status updates, 202 on save) is a success with nil data.
- Non-JSON error bodies (a proxy's 502 page) are kept in `details` truncated to 300 characters.
- `-h`/`--help`/`help` on a group (`entry --help`) or a leaf (`entry list -h`) prints the usage
  to stderr and exits 0 (`errHelp`). Every command rejects stray positionals, so a mistyped
  flag cannot pass silently.
- `MINIFLUX_URL` must be `http(s)://host`; `url.ParseRequestURI` accepted `localhost:8080` and
  the failure then surfaced as a transport error without a `fix`.
- The tag stripper understands quoted attributes, so a `>` inside `title="a>b"` does not leak
  text. Nested `<svg>` inside `<svg>` still leaves a tail; not worth a parser.
