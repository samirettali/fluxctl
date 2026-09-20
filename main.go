package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

const version = "0.1.0"

func main() {
	if err := run(os.Args[1:]); err != nil {
		if errors.Is(err, errHelp) {
			printUsage()
			return
		}
		stderr := json.NewEncoder(os.Stderr)
		stderr.SetEscapeHTML(false)
		var authErr *authError
		var apiErr *APIError
		if errors.As(err, &authErr) {
			payload := map[string]any{"error": authErr.Message, "fix": authErr.Fix}
			if authErr.Cause != nil {
				payload["details"] = authErr.Cause.Error()
			}
			_ = stderr.Encode(payload)
		} else if errors.As(err, &apiErr) {
			_ = stderr.Encode(map[string]any{
				"error":   apiErr.Message,
				"status":  apiErr.Status,
				"details": apiErr.Details,
			})
		} else {
			_ = stderr.Encode(map[string]string{"error": err.Error()})
		}
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}

	switch args[0] {
	case "me":
		return runMe(args[1:])
	case "category":
		return runCategory(args[1:])
	case "feed":
		return runFeed(args[1:])
	case "entry":
		return runEntry(args[1:])
	case "opml", "icon", "enclosure", "server", "integration":
		return runUserCommand(args[0], args[1:])
	case "version", "--version", "-v":
		fs := newFlagSet("version")
		if err := parseFlags(fs, args[1:]); err != nil {
			return err
		}
		if err := noArgs(fs); err != nil {
			return err
		}
		return writeJSON(map[string]string{"version": version})
	case "help", "--help", "-h":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func printUsage() {
	fmt.Fprint(os.Stderr, `fluxctl manages your Miniflux subscriptions and articles through its API.

Usage:
  fluxctl me
  fluxctl category list
  fluxctl feed list [--category ID] [--full]
  fluxctl feed get ID [--full]
  fluxctl feed counters
  fluxctl feed discover --url URL [--input FILE] [--full]
  fluxctl feed create --feed-url URL [--category ID] [--input FILE] [--full]
  fluxctl feed update ID [--title TITLE] [--category ID] [--disabled=false] [--input FILE] [--full]
  fluxctl feed delete ID
  fluxctl feed refresh ID | --all
  fluxctl feed mark-all-read ID
  fluxctl feed icon ID [--full]
  fluxctl category create --title TITLE [--hide-globally] [--input FILE] [--full]
  fluxctl category update ID [--title TITLE] [--hide-globally=false] [--input FILE] [--full]
  fluxctl category delete ID
  fluxctl category refresh ID
  fluxctl category mark-all-read ID
  fluxctl entry list [--status unread|read|all] [--starred] [--feed ID] [--category ID]
                     [--since DURATION|RFC3339] [--until DURATION|RFC3339]
                     [--published-since DURATION|RFC3339] [--published-until DURATION|RFC3339]
                     [--search QUERY] [--limit N] [--offset N] [--order FIELD] [--direction asc|desc]
                     [--tag TAG ...] [--globally-visible] [--before-id ID] [--after-id ID]
                     [--content] [--full]
  fluxctl entry get ID [--html] [--full]
  fluxctl entry fetch ID [--html]
  fluxctl entry read ID...
  fluxctl entry unread ID...
  fluxctl entry star ID...
  fluxctl entry unstar ID...
  fluxctl entry save ID...
  fluxctl entry update ID [--title TITLE] [--content-file FILE] [--input FILE] [--full]
  fluxctl entry import FEED_ID --url URL [--content-file FILE] [--status read|unread] [--input FILE] [--full]
  fluxctl entry fetch-update ID [--html]
  fluxctl entry mark-all-read
  fluxctl entry flush-history
  fluxctl entry ids [--status read|unread|all] [--starred=false] [--limit 1..10000] [--offset N] [--full]
  fluxctl enclosure get ID [--full]
  fluxctl enclosure update ID --media-progression SECONDS
  fluxctl icon get ID [--full]
  fluxctl opml import --input FILE
  fluxctl opml export --output NEW_FILE
  fluxctl integration status [--full]
  fluxctl server version [--full]
  fluxctl version

Configuration:
  MINIFLUX_URL       base URL of the instance (https://rss.example.com)
  MINIFLUX_API_KEY   API key from Settings > API Keys
  When either is unset, both are read from the rbw entry "miniflux-api-key"
  (the password is the key, the URI the base URL), if the vault is unlocked.

stdout is JSON; errors are JSON on stderr. Flags may precede or follow IDs.
Mutations are explicit; delete/flush-history are destructive and do not prompt.
Omitted update fields are unchanged; use --boolean=false to explicitly clear a flag.
Feed credentials (username/password/cookie/proxy_url) are accepted only in --input JSON files.
--full preserves API fields except subscription secrets, which are always redacted.
OPML export creates a new private file; it never overwrites. No automatic mutation retries.
See docs/user-api.md for every subscription field, file format and version contract.
`)
}
