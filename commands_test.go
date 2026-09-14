package main

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseTime(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		in   string
		want int64
		err  bool
	}{
		{"", 0, false},
		{"24h", now.Add(-24 * time.Hour).Unix(), false},
		{"7d", now.Add(-7 * 24 * time.Hour).Unix(), false},
		{"2w", now.Add(-14 * 24 * time.Hour).Unix(), false},
		{"2026-09-13T10:00:00Z", time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC).Unix(), false},
		{"yesterday", 0, true},
		{"-1h", 0, true},
		{"d", 0, true},
		{"1.5d", 0, true},
		{"-2w", 0, true},
	}
	for _, tc := range cases {
		got, err := parseTime(tc.in, now)
		if (err != nil) != tc.err {
			t.Fatalf("parseTime(%q) error = %v, want error %v", tc.in, err, tc.err)
		}
		if got != tc.want {
			t.Fatalf("parseTime(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestEntryListQuery(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	o := entryListOptions{status: "unread", starred: true, feedID: 7, categoryID: 3, since: "1d", until: "1h",
		publishedSince: "2w", search: "kafka", limit: 20, offset: 40, order: "published_at", direction: "desc"}
	q, err := o.query(now)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"status": "unread", "starred": "true", "feed_id": "7", "category_id": "3",
		"changed_after":   strconv.FormatInt(now.Add(-24*time.Hour).Unix(), 10),
		"changed_before":  strconv.FormatInt(now.Add(-time.Hour).Unix(), 10),
		"published_after": strconv.FormatInt(now.Add(-14*24*time.Hour).Unix(), 10),
		"search":          "kafka", "limit": "20", "offset": "40", "order": "published_at", "direction": "desc",
	}
	for key, value := range want {
		if q.Get(key) != value {
			t.Errorf("%s = %q, want %q", key, q.Get(key), value)
		}
	}
	if q.Has("published_before") {
		t.Error("published_before should be absent when --published-until is empty")
	}

	all := entryListOptions{status: "all", order: "id", direction: "asc"}
	q, err = all.query(now)
	if err != nil {
		t.Fatal(err)
	}
	if q.Has("status") {
		t.Errorf("status all should send no status filter: %v", q)
	}
	if q.Get("limit") != "0" {
		t.Errorf("limit 0 must be sent explicitly, Miniflux defaults to 100 otherwise: %v", q)
	}

	for _, bad := range []entryListOptions{
		{status: "nope", order: "id", direction: "asc"},
		{status: "removed", order: "id", direction: "asc"},
		{status: "unread", order: "feed_title", direction: "asc"},
		{status: "unread", order: "id", direction: "asc", limit: maxEntryLimit + 1},
		{status: "unread", order: "id", direction: "up"},
		{status: "unread", order: "id", direction: "asc", limit: -1},
		{status: "unread", order: "id", direction: "asc", since: "soon"},
	} {
		if _, err := bad.query(now); err == nil {
			t.Errorf("expected an error for %+v", bad)
		}
	}
}

func TestParseFlagsInterspersed(t *testing.T) {
	fs := newFlagSet("x")
	full := fs.Bool("full", false, "")
	if err := parseFlags(fs, []string{"42", "--full", "43"}); err != nil {
		t.Fatal(err)
	}
	if !*full {
		t.Error("--full after a positional was ignored")
	}
	if got := strings.Join(fs.Args(), ","); got != "42,43" {
		t.Errorf("positionals = %q, want 42,43", got)
	}
}

func TestParseFlagsHelp(t *testing.T) {
	fs := newFlagSet("x")
	if err := parseFlags(fs, []string{"--help"}); !errors.Is(err, errHelp) {
		t.Errorf("--help should surface errHelp, got %v", err)
	}
}

func TestGroupHelp(t *testing.T) {
	for _, args := range [][]string{{"entry", "--help"}, {"feed", "-h"}, {"category", "help"}, {"entry", "list", "-h"}} {
		if err := run(args); !errors.Is(err, errHelp) {
			t.Errorf("%v should surface errHelp, got %v", args, err)
		}
	}
}

func TestStrayPositionals(t *testing.T) {
	for _, args := range [][]string{{"me", "x"}, {"category", "list", "x"}, {"feed", "list", "x"}, {"feed", "counters", "x"}, {"entry", "list", "x"}} {
		if err := run(args); err == nil || !strings.Contains(err.Error(), "unexpected argument") {
			t.Errorf("%v should reject the stray argument, got %v", args, err)
		}
	}
}

func TestParseIDs(t *testing.T) {
	if _, err := parseIDs("x", nil); err == nil {
		t.Error("no IDs should fail")
	}
	if _, err := parseIDs("x", []string{"1", "abc"}); err == nil {
		t.Error("non-numeric ID should fail")
	}
	if _, err := parseIDs("x", []string{"0"}); err == nil {
		t.Error("zero ID should fail")
	}
	ids, err := parseIDs("x", []string{"1", "2"})
	if err != nil || len(ids) != 2 || ids[1] != 2 {
		t.Errorf("parseIDs = %v, %v", ids, err)
	}
}
