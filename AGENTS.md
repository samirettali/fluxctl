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
  backreferences, so the script/style dropper lists each tag explicitly.
- **`--since`/`--until` map to Miniflux's `after`/`before`, which filter on when Miniflux
  fetched the entry, not on `published_at`.** That is the useful meaning for "what came in since
  yesterday" and it survives feeds with bogus publication dates (there are entries dated 1970).
  Both accept `24h`, `7d`, `2w` or an RFC 3339 timestamp.
- `entry list` defaults to `--status unread`, `--order published_at --direction desc`,
  `--limit 50`. `--status all` drops the filter, `--limit 0` asks for everything.
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
  `{"starred", "changed", "skipped", "failed"}`; one failure does not abort the rest.
- `read`/`unread` are one request for all IDs (`PUT /entries` takes a list), `save` is one
  request per entry (`POST /entries/{id}/save`), which sends it to the third-party integration
  configured in Miniflux — linkding on Samir's instance.
- Flags may come before or after positional arguments (`parseFlags` re-parses after each
  positional), because agents write `entry get 42 --full` as often as the other way round.
- A 2xx with an empty body (204 on status updates, 202 on save) is a success with nil data.
