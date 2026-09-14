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
	case "version", "--version", "-v":
		return writeJSON(map[string]string{"version": version})
	case "help", "--help", "-h":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func printUsage() {
	fmt.Fprint(os.Stderr, `fluxctl reads a Miniflux instance through its API.

Usage:
  fluxctl me
  fluxctl category list
  fluxctl feed list [--category ID] [--full]
  fluxctl feed get ID [--full]
  fluxctl feed counters
  fluxctl entry list [--status unread|read|all] [--starred] [--feed ID] [--category ID]
                     [--since DURATION|RFC3339] [--until DURATION|RFC3339]
                     [--published-since DURATION|RFC3339] [--published-until DURATION|RFC3339]
                     [--search QUERY] [--limit N] [--offset N] [--order FIELD] [--direction asc|desc]
                     [--content] [--full]
  fluxctl entry get ID [--html] [--full]
  fluxctl entry fetch ID [--html]
  fluxctl entry read ID...
  fluxctl entry unread ID...
  fluxctl entry star ID...
  fluxctl entry unstar ID...
  fluxctl entry save ID...
  fluxctl version

Configuration:
  MINIFLUX_URL       base URL of the instance (https://rss.example.com)
  MINIFLUX_API_KEY   API key from Settings > API Keys
  When either is unset, both are read from the rbw entry "miniflux-api-key"
  (the password is the key, the URI the base URL), if the vault is unlocked.

stdout is JSON; errors are JSON on stderr.
`)
}
