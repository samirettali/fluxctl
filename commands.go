package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	return fs
}

// parseFlags accepts flags before and after positional arguments, so `entry get 42 --full`
// works as well as `entry get --full 42`. The positionals end up in fs.Args() in order.
func parseFlags(fs *flag.FlagSet, args []string) error {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return fmt.Errorf("%s: %w", fs.Name(), err)
		}
		rest := fs.Args()
		if len(rest) == 0 {
			break
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
	return fs.Parse(positional)
}

func parseIDs(name string, args []string) ([]int64, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("%s: at least one ID is required", name)
	}
	ids := make([]int64, 0, len(args))
	for _, arg := range args {
		id, err := strconv.ParseInt(arg, 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("%s: invalid ID %q", name, arg)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func runMe(args []string) error {
	fs := newFlagSet("me")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	client, err := newMinifluxClient()
	if err != nil {
		return err
	}
	data, err := client.request("GET", "/me", nil, nil)
	if err != nil {
		return err
	}
	return writeJSON(data)
}

func runCategory(args []string) error {
	if len(args) == 0 {
		return errors.New("category: subcommand required (list)")
	}
	switch args[0] {
	case "list":
		fs := newFlagSet("category list")
		full := fs.Bool("full", false, "return Miniflux's own objects")
		if err := parseFlags(fs, args[1:]); err != nil {
			return err
		}
		client, err := newMinifluxClient()
		if err != nil {
			return err
		}
		data, err := client.request("GET", "/categories", url.Values{"counts": {"true"}}, nil)
		if err != nil {
			return err
		}
		if *full {
			return writeJSON(data)
		}
		categories, err := decodeCategories(data)
		if err != nil {
			return err
		}
		return writeJSON(categories)
	default:
		return fmt.Errorf("category: unknown subcommand %q", args[0])
	}
}

func runFeed(args []string) error {
	if len(args) == 0 {
		return errors.New("feed: subcommand required (list, get, counters)")
	}
	switch args[0] {
	case "list":
		fs := newFlagSet("feed list")
		categoryID := fs.Int64("category", 0, "only feeds in this category")
		full := fs.Bool("full", false, "return Miniflux's own objects")
		if err := parseFlags(fs, args[1:]); err != nil {
			return err
		}
		client, err := newMinifluxClient()
		if err != nil {
			return err
		}
		path := "/feeds"
		if *categoryID > 0 {
			path = fmt.Sprintf("/categories/%d/feeds", *categoryID)
		}
		data, err := client.request("GET", path, nil, nil)
		if err != nil {
			return err
		}
		if *full {
			return writeJSON(data)
		}
		feeds, err := decodeFeeds(data)
		if err != nil {
			return err
		}
		return writeJSON(feeds)
	case "get":
		fs := newFlagSet("feed get")
		full := fs.Bool("full", false, "return Miniflux's own object")
		if err := parseFlags(fs, args[1:]); err != nil {
			return err
		}
		ids, err := parseIDs("feed get", fs.Args())
		if err != nil {
			return err
		}
		if len(ids) != 1 {
			return errors.New("feed get: exactly one ID is required")
		}
		client, err := newMinifluxClient()
		if err != nil {
			return err
		}
		data, err := client.request("GET", fmt.Sprintf("/feeds/%d", ids[0]), nil, nil)
		if err != nil {
			return err
		}
		if *full {
			return writeJSON(data)
		}
		f, err := decodeFeed(data)
		if err != nil {
			return err
		}
		return writeJSON(f)
	case "counters":
		fs := newFlagSet("feed counters")
		if err := parseFlags(fs, args[1:]); err != nil {
			return err
		}
		client, err := newMinifluxClient()
		if err != nil {
			return err
		}
		data, err := client.request("GET", "/feeds/counters", nil, nil)
		if err != nil {
			return err
		}
		return writeJSON(data)
	default:
		return fmt.Errorf("feed: unknown subcommand %q", args[0])
	}
}

var validStatuses = map[string]bool{"unread": true, "read": true, "removed": true, "all": true}
var validOrders = map[string]bool{"id": true, "status": true, "published_at": true, "category_title": true, "category_id": true}

// parseTime accepts a relative duration ("24h", "7d", "2w") or an RFC 3339 timestamp and
// returns a Unix timestamp, which is what Miniflux's before/after filters take.
func parseTime(value string, now time.Time) (int64, error) {
	if value == "" {
		return 0, nil
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.Unix(), nil
	}
	if d, err := parseDuration(value); err == nil {
		return now.Add(-d).Unix(), nil
	}
	return 0, fmt.Errorf("invalid time %q: use a duration like 24h, 7d, 2w or an RFC 3339 timestamp", value)
}

func parseDuration(value string) (time.Duration, error) {
	if strings.HasSuffix(value, "d") || strings.HasSuffix(value, "w") {
		n, err := strconv.Atoi(value[:len(value)-1])
		if err != nil || n < 0 {
			return 0, fmt.Errorf("invalid duration %q", value)
		}
		days := n
		if strings.HasSuffix(value, "w") {
			days *= 7
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("invalid duration %q", value)
	}
	return d, nil
}

type entryListOptions struct {
	status     string
	starred    bool
	feedID     int64
	categoryID int64
	since      string
	until      string
	search     string
	limit      int
	offset     int
	order      string
	direction  string
}

func (o entryListOptions) query(now time.Time) (url.Values, error) {
	if !validStatuses[o.status] {
		return nil, fmt.Errorf("entry list: invalid --status %q (unread, read, removed, all)", o.status)
	}
	if !validOrders[o.order] {
		return nil, fmt.Errorf("entry list: invalid --order %q (id, status, published_at, category_title, category_id)", o.order)
	}
	if o.direction != "asc" && o.direction != "desc" {
		return nil, fmt.Errorf("entry list: invalid --direction %q (asc, desc)", o.direction)
	}
	if o.limit < 0 || o.offset < 0 {
		return nil, errors.New("entry list: --limit and --offset must be >= 0")
	}
	q := url.Values{}
	if o.status != "all" {
		q.Set("status", o.status)
	}
	if o.starred {
		q.Set("starred", "true")
	}
	if o.feedID > 0 {
		q.Set("feed_id", strconv.FormatInt(o.feedID, 10))
	}
	if o.categoryID > 0 {
		q.Set("category_id", strconv.FormatInt(o.categoryID, 10))
	}
	if o.search != "" {
		q.Set("search", o.search)
	}
	after, err := parseTime(o.since, now)
	if err != nil {
		return nil, fmt.Errorf("entry list --since: %w", err)
	}
	if after > 0 {
		q.Set("after", strconv.FormatInt(after, 10))
	}
	before, err := parseTime(o.until, now)
	if err != nil {
		return nil, fmt.Errorf("entry list --until: %w", err)
	}
	if before > 0 {
		q.Set("before", strconv.FormatInt(before, 10))
	}
	if o.limit > 0 {
		q.Set("limit", strconv.Itoa(o.limit))
	}
	if o.offset > 0 {
		q.Set("offset", strconv.Itoa(o.offset))
	}
	q.Set("order", o.order)
	q.Set("direction", o.direction)
	return q, nil
}

func runEntry(args []string) error {
	if len(args) == 0 {
		return errors.New("entry: subcommand required (list, get, fetch, read, unread, star, unstar, save)")
	}
	switch args[0] {
	case "list":
		return runEntryList(args[1:])
	case "get":
		return runEntryGet(args[1:])
	case "fetch":
		return runEntryFetch(args[1:])
	case "read":
		return runEntryStatus("read", args[1:])
	case "unread":
		return runEntryStatus("unread", args[1:])
	case "star":
		return runEntryStar(true, args[1:])
	case "unstar":
		return runEntryStar(false, args[1:])
	case "save":
		return runEntrySave(args[1:])
	default:
		return fmt.Errorf("entry: unknown subcommand %q", args[0])
	}
}

func runEntryList(args []string) error {
	fs := newFlagSet("entry list")
	var o entryListOptions
	fs.StringVar(&o.status, "status", "unread", "unread, read, removed or all")
	fs.BoolVar(&o.starred, "starred", false, "only starred entries")
	fs.Int64Var(&o.feedID, "feed", 0, "only entries of this feed")
	fs.Int64Var(&o.categoryID, "category", 0, "only entries in this category")
	fs.StringVar(&o.since, "since", "", "entries fetched after this duration ago or RFC 3339 time")
	fs.StringVar(&o.until, "until", "", "entries fetched before this duration ago or RFC 3339 time")
	fs.StringVar(&o.search, "search", "", "full-text search")
	fs.IntVar(&o.limit, "limit", 50, "page size, 0 for everything")
	fs.IntVar(&o.offset, "offset", 0, "page offset")
	fs.StringVar(&o.order, "order", "published_at", "id, status, published_at, category_title or category_id")
	fs.StringVar(&o.direction, "direction", "desc", "asc or desc")
	content := fs.Bool("content", false, "include the content of each entry as plain text")
	full := fs.Bool("full", false, "return Miniflux's own objects")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("entry list: unexpected argument %q", fs.Arg(0))
	}
	query, err := o.query(time.Now())
	if err != nil {
		return err
	}
	client, err := newMinifluxClient()
	if err != nil {
		return err
	}
	data, err := client.request("GET", "/entries", query, nil)
	if err != nil {
		return err
	}
	if *full {
		return writeJSON(data)
	}
	mode := ""
	if *content {
		mode = "text"
	}
	list, err := decodeEntries(data, mode)
	if err != nil {
		return err
	}
	return writeJSON(list)
}

func runEntryGet(args []string) error {
	fs := newFlagSet("entry get")
	asHTML := fs.Bool("html", false, "keep the content as HTML instead of plain text")
	full := fs.Bool("full", false, "return Miniflux's own object")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	ids, err := parseIDs("entry get", fs.Args())
	if err != nil {
		return err
	}
	if len(ids) != 1 {
		return errors.New("entry get: exactly one ID is required")
	}
	client, err := newMinifluxClient()
	if err != nil {
		return err
	}
	data, err := client.request("GET", fmt.Sprintf("/entries/%d", ids[0]), nil, nil)
	if err != nil {
		return err
	}
	if *full {
		return writeJSON(data)
	}
	mode := "text"
	if *asHTML {
		mode = "html"
	}
	e, err := decodeEntry(data, mode)
	if err != nil {
		return err
	}
	return writeJSON(e)
}

// runEntryFetch asks Miniflux to download the original page, for feeds that only ship a
// summary. The answer is {"content": ...}, kept as is except for the HTML flattening.
func runEntryFetch(args []string) error {
	fs := newFlagSet("entry fetch")
	asHTML := fs.Bool("html", false, "keep the content as HTML instead of plain text")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	ids, err := parseIDs("entry fetch", fs.Args())
	if err != nil {
		return err
	}
	if len(ids) != 1 {
		return errors.New("entry fetch: exactly one ID is required")
	}
	client, err := newMinifluxClient()
	if err != nil {
		return err
	}
	data, err := client.request("GET", fmt.Sprintf("/entries/%d/fetch-content", ids[0]), nil, nil)
	if err != nil {
		return err
	}
	if *asHTML {
		return writeJSON(data)
	}
	var body struct {
		Content string `json:"content"`
	}
	if err := decodeInto(data, &body, "fetched content"); err != nil {
		return err
	}
	return writeJSON(map[string]any{"id": ids[0], "content": htmlToText(body.Content)})
}

func runEntryStatus(status string, args []string) error {
	name := "entry " + status
	fs := newFlagSet(name)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	ids, err := parseIDs(name, fs.Args())
	if err != nil {
		return err
	}
	client, err := newMinifluxClient()
	if err != nil {
		return err
	}
	if _, err := client.request("PUT", "/entries", nil, map[string]any{"entry_ids": ids, "status": status}); err != nil {
		return err
	}
	return writeJSON(map[string]any{"status": status, "entries": ids})
}

// runEntryStar is idempotent even though Miniflux only exposes a toggle: it reads each
// entry first and flips only the ones that are not already in the requested state.
func runEntryStar(star bool, args []string) error {
	name := "entry star"
	if !star {
		name = "entry unstar"
	}
	fs := newFlagSet(name)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	ids, err := parseIDs(name, fs.Args())
	if err != nil {
		return err
	}
	client, err := newMinifluxClient()
	if err != nil {
		return err
	}
	changed, skipped := []int64{}, []int64{}
	failed := []map[string]any{}
	for _, id := range ids {
		data, err := client.request("GET", fmt.Sprintf("/entries/%d", id), nil, nil)
		if err != nil {
			failed = append(failed, map[string]any{"id": id, "error": err.Error()})
			continue
		}
		var current struct {
			Starred bool `json:"starred"`
		}
		if err := decodeInto(data, &current, "entry"); err != nil {
			failed = append(failed, map[string]any{"id": id, "error": err.Error()})
			continue
		}
		if current.Starred == star {
			skipped = append(skipped, id)
			continue
		}
		if _, err := client.request("PUT", fmt.Sprintf("/entries/%d/bookmark", id), nil, nil); err != nil {
			failed = append(failed, map[string]any{"id": id, "error": err.Error()})
			continue
		}
		changed = append(changed, id)
	}
	return writeJSON(map[string]any{"starred": star, "changed": changed, "skipped": skipped, "failed": failed})
}

// runEntrySave sends entries to the third-party integration configured in Miniflux.
func runEntrySave(args []string) error {
	fs := newFlagSet("entry save")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	ids, err := parseIDs("entry save", fs.Args())
	if err != nil {
		return err
	}
	client, err := newMinifluxClient()
	if err != nil {
		return err
	}
	saved := []int64{}
	failed := []map[string]any{}
	for _, id := range ids {
		if _, err := client.request("POST", fmt.Sprintf("/entries/%d/save", id), nil, nil); err != nil {
			failed = append(failed, map[string]any{"id": id, "error": err.Error()})
			continue
		}
		saved = append(saved, id)
	}
	return writeJSON(map[string]any{"saved": saved, "failed": failed})
}
