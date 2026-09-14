package main

import (
	"encoding/json"
	"fmt"
)

// The trimmed shapes keep Miniflux's own envelopes (a bare array for feeds and categories,
// {"total", "entries"} for entries) and only slim the objects inside: raw entries carry the
// whole feed object plus the HTML content, which is most of the bytes an agent pays for.

// category is both the wire shape and the trimmed one: Miniflux's category object is
// already small, so decoding into this struct drops only user_id and hide_globally.
type category struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	FeedCount   *int   `json:"feed_count,omitempty"`
	TotalUnread *int   `json:"total_unread,omitempty"`
}

type rawFeed struct {
	ID                  int64    `json:"id"`
	Title               string   `json:"title"`
	SiteURL             string   `json:"site_url"`
	FeedURL             string   `json:"feed_url"`
	CheckedAt           string   `json:"checked_at"`
	Disabled            bool     `json:"disabled"`
	ParsingErrorCount   int      `json:"parsing_error_count"`
	ParsingErrorMessage string   `json:"parsing_error_message"`
	Category            category `json:"category"`
}

type rawEnclosure struct {
	URL      string `json:"url"`
	MimeType string `json:"mime_type"`
}

type rawEntry struct {
	ID          int64          `json:"id"`
	FeedID      int64          `json:"feed_id"`
	Status      string         `json:"status"`
	Title       string         `json:"title"`
	URL         string         `json:"url"`
	Author      string         `json:"author"`
	PublishedAt string         `json:"published_at"`
	CreatedAt   string         `json:"created_at"`
	Content     string         `json:"content"`
	Starred     bool           `json:"starred"`
	ReadingTime int            `json:"reading_time"`
	Feed        rawFeed        `json:"feed"`
	Enclosures  []rawEnclosure `json:"enclosures"`
	Tags        []string       `json:"tags"`
}

type rawEntries struct {
	Total   int        `json:"total"`
	Entries []rawEntry `json:"entries"`
}

type namedRef struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

type feed struct {
	ID           int64    `json:"id"`
	Title        string   `json:"title"`
	SiteURL      string   `json:"site_url"`
	FeedURL      string   `json:"feed_url"`
	Category     namedRef `json:"category"`
	CheckedAt    string   `json:"checked_at"`
	Disabled     bool     `json:"disabled"`
	ParsingError string   `json:"parsing_error,omitempty"`
}

type entry struct {
	ID          int64       `json:"id"`
	Title       string      `json:"title"`
	URL         string      `json:"url"`
	Author      string      `json:"author,omitempty"`
	PublishedAt string      `json:"published_at"`
	CreatedAt   string      `json:"created_at"`
	Status      string      `json:"status"`
	Starred     bool        `json:"starred"`
	ReadingTime int         `json:"reading_time"`
	Feed        namedRef    `json:"feed"`
	Category    namedRef    `json:"category"`
	Enclosures  []enclosure `json:"enclosures,omitempty"`
	Tags        []string    `json:"tags,omitempty"`
	Content     *string     `json:"content,omitempty"`
}

// enclosure is how a podcast episode or a video reaches the agent: the entry URL is often
// the page, the enclosure the media itself.
type enclosure struct {
	URL      string `json:"url"`
	MimeType string `json:"mime_type"`
}

type entries struct {
	Total   int     `json:"total"`
	Entries []entry `json:"entries"`
}

func trimFeed(raw rawFeed) feed {
	f := feed{
		ID:        raw.ID,
		Title:     raw.Title,
		SiteURL:   raw.SiteURL,
		FeedURL:   raw.FeedURL,
		Category:  namedRef{ID: raw.Category.ID, Title: raw.Category.Title},
		CheckedAt: raw.CheckedAt,
		Disabled:  raw.Disabled,
	}
	if raw.ParsingErrorCount > 0 {
		f.ParsingError = fmt.Sprintf("%d consecutive failures: %s", raw.ParsingErrorCount, raw.ParsingErrorMessage)
	}
	return f
}

// trimEntry drops the content unless asked for; contentMode is "" (omit), "text" or "html".
func trimEntry(raw rawEntry, contentMode string) entry {
	e := entry{
		ID:          raw.ID,
		Title:       raw.Title,
		URL:         raw.URL,
		Author:      raw.Author,
		PublishedAt: raw.PublishedAt,
		CreatedAt:   raw.CreatedAt,
		Status:      raw.Status,
		Starred:     raw.Starred,
		ReadingTime: raw.ReadingTime,
		Feed:        namedRef{ID: raw.Feed.ID, Title: raw.Feed.Title},
		Category:    namedRef{ID: raw.Feed.Category.ID, Title: raw.Feed.Category.Title},
		Tags:        raw.Tags,
	}
	for _, enc := range raw.Enclosures {
		e.Enclosures = append(e.Enclosures, enclosure{URL: enc.URL, MimeType: enc.MimeType})
	}
	switch contentMode {
	case "text":
		text := htmlToText(raw.Content)
		e.Content = &text
	case "html":
		content := raw.Content
		e.Content = &content
	}
	return e
}

func decodeCategories(data json.RawMessage) ([]category, error) {
	categories := []category{}
	if err := json.Unmarshal(data, &categories); err != nil {
		return nil, fmt.Errorf("decoding categories: %w", err)
	}
	return categories, nil
}

func decodeFeeds(data json.RawMessage) ([]feed, error) {
	var raw []rawFeed
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("decoding feeds: %w", err)
	}
	out := make([]feed, 0, len(raw))
	for _, f := range raw {
		out = append(out, trimFeed(f))
	}
	return out, nil
}

func decodeFeed(data json.RawMessage) (feed, error) {
	var raw rawFeed
	if err := json.Unmarshal(data, &raw); err != nil {
		return feed{}, fmt.Errorf("decoding feed: %w", err)
	}
	return trimFeed(raw), nil
}

func decodeEntries(data json.RawMessage, contentMode string) (entries, error) {
	var raw rawEntries
	if err := json.Unmarshal(data, &raw); err != nil {
		return entries{}, fmt.Errorf("decoding entries: %w", err)
	}
	out := entries{Total: raw.Total, Entries: make([]entry, 0, len(raw.Entries))}
	for _, e := range raw.Entries {
		out.Entries = append(out.Entries, trimEntry(e, contentMode))
	}
	return out, nil
}

func decodeEntry(data json.RawMessage, contentMode string) (entry, error) {
	var raw rawEntry
	if err := json.Unmarshal(data, &raw); err != nil {
		return entry{}, fmt.Errorf("decoding entry: %w", err)
	}
	return trimEntry(raw, contentMode), nil
}

func decodeInto(data json.RawMessage, target any, what string) error {
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decoding %s: %w", what, err)
	}
	return nil
}
