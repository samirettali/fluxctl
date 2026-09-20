package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Fields mirror the public API, not the much larger response objects. Only visited
// flags are serialized: omitted values and explicit false/zero are different.
type requestFields map[string]string

func fields(stringsList, boolsList, intsList string) requestFields {
	result := requestFields{}
	for kind, list := range map[string]string{"string": stringsList, "bool": boolsList, "int": intsList} {
		for _, name := range strings.Fields(list) {
			result[name] = kind
		}
	}
	return result
}

func feedFields(action string) requestFields {
	result := fields("feed_url user_agent scraper_rules rewrite_rules blocklist_rules keeplist_rules block_filter_entry_rules keep_filter_entry_rules urlrewrite_rules", "crawler ignore_entry_updates disabled no_media_player ignore_http_cache allow_self_signed_certificates fetch_via_proxy hide_globally disable_http2", "category_id")
	if action == "update" {
		for _, name := range []string{"site_url", "title", "description"} {
			result[name] = "string"
		}
	}
	for _, name := range []string{"username", "password", "cookie", "proxy_url"} {
		result[name] = "secret"
	}
	return result
}

func discoveryFields() requestFields {
	return fields("url user_agent", "fetch_via_proxy allow_self_signed_certificates disable_http2", "")
}

func flagName(field string) string {
	if field == "category_id" {
		return "category"
	}
	return strings.ReplaceAll(field, "_", "-")
}

func registerFields(fs *flag.FlagSet, schema requestFields) {
	for field, kind := range schema {
		name := flagName(field)
		switch kind {
		case "string":
			fs.String(name, "", "API field "+field)
		case "bool":
			fs.Bool(name, false, "API field "+field+" (use =false to clear)")
		case "int":
			fs.Int64(name, 0, "API field "+field)
		}
	}
}

func readRequestFields(fs *flag.FlagSet, schema requestFields, input string) (map[string]any, error) {
	body := map[string]any{}
	if input != "" {
		data, err := readInputFile(input)
		if err != nil {
			return nil, errors.New("cannot read input file")
		}
		var raw map[string]json.RawMessage
		if json.Unmarshal(data, &raw) != nil || raw == nil {
			return nil, errors.New("input must be a JSON object")
		}
		for key, value := range raw {
			kind, ok := schema[key]
			if !ok {
				return nil, errors.New("input contains an unsupported field")
			}
			if string(value) == "null" {
				return nil, errors.New("input fields must not be null")
			}
			var target any
			switch kind {
			case "string", "secret":
				target = new(string)
			case "bool":
				target = new(bool)
			case "int":
				target = new(int64)
			case "strings":
				target = new([]string)
			}
			if json.Unmarshal(value, target) != nil {
				return nil, fmt.Errorf("invalid type for input field %s", key)
			}
			switch v := target.(type) {
			case *string:
				body[key] = *v
			case *bool:
				body[key] = *v
			case *int64:
				body[key] = *v
			case *[]string:
				body[key] = *v
			}
		}
	}
	var flagErr error
	fs.Visit(func(f *flag.Flag) {
		for field, kind := range schema {
			if f.Name != flagName(field) || kind == "secret" || kind == "strings" {
				continue
			}
			value := f.Value.(flag.Getter).Get()
			if s, ok := value.(string); ok && (field == "url" || strings.HasSuffix(field, "_url")) {
				parsed, err := url.Parse(s)
				if (err == nil && parsed.User != nil) || (err != nil && strings.Contains(s, "@")) {
					flagErr = fmt.Errorf("--%s: put authenticated URLs in the JSON --input file, not command-line flags", f.Name)
				}
			}
			body[field] = value
		}
	})
	return body, flagErr
}

func oneID(fs *flag.FlagSet) (int64, error) {
	ids, err := parseIDs(fs.Name(), fs.Args())
	if err != nil {
		return 0, err
	}
	if len(ids) != 1 {
		return 0, fmt.Errorf("%s: exactly one ID is required", fs.Name())
	}
	return ids[0], nil
}

func runUserCommand(group string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s: subcommand required", group)
	}
	if isHelp(args[0]) {
		return errHelp
	}
	action := args[0]
	if group == "opml" {
		return runOPML(action, args[1:])
	}
	if group == "entry" && action == "ids" {
		return runEntryIDs(args[1:])
	}
	fs := newFlagSet(group + " " + action)
	schema := requestFields{}
	method, path, response := "", "", "receipt"
	needsID := false
	switch group + " " + action {
	case "feed discover":
		method, path, response = "POST", "/discover", "discovery"
		schema = discoveryFields()
		for _, k := range []string{"username", "password", "cookie", "proxy_url"} {
			schema[k] = "secret"
		}
	case "feed create":
		method, path, response, schema = "POST", "/feeds", "feed_id", feedFields(action)
	case "feed update":
		method, path, response, schema, needsID = "PUT", "/feeds/%d", "feed", feedFields(action), true
	case "feed delete":
		method, path, needsID = "DELETE", "/feeds/%d", true
	case "feed refresh":
		method, path = "PUT", "/feeds/%d/refresh"
	case "feed mark-all-read":
		method, path, needsID = "PUT", "/feeds/%d/mark-all-as-read", true
	case "feed icon":
		method, path, response, needsID = "GET", "/feeds/%d/icon", "icon", true
	case "category create":
		method, path, response, schema = "POST", "/categories", "category", fields("title", "hide_globally", "")
	case "category update":
		method, path, response, schema, needsID = "PUT", "/categories/%d", "category", fields("title", "hide_globally", ""), true
	case "category delete":
		method, path, needsID = "DELETE", "/categories/%d", true
	case "category refresh":
		method, path, needsID = "PUT", "/categories/%d/refresh", true
	case "category mark-all-read":
		method, path, needsID = "PUT", "/categories/%d/mark-all-as-read", true
	case "entry update":
		method, path, response, schema, needsID = "PUT", "/entries/%d", "entry", fields("title content", "", ""), true
	case "entry import":
		method, path, response, schema, needsID = "POST", "/feeds/%d/entries/import", "id", fields("url title content author comments_url status external_id", "starred", "published_at"), true
		schema["tags"] = "strings"
	case "entry flush-history":
		method, path = "DELETE", "/flush-history"
	case "entry mark-all-read":
		method, path = "PUT", "/users/%d/mark-all-as-read"
	case "enclosure get":
		method, path, response, needsID = "GET", "/enclosures/%d", "enclosure", true
	case "enclosure update":
		method, path, schema, needsID = "PUT", "/enclosures/%d", fields("", "", "media_progression"), true
	case "icon get":
		method, path, response, needsID = "GET", "/icons/%d", "icon", true
	case "integration status":
		method, path, response = "GET", "/integrations/status", "integration"
	case "server version":
		method, path, response = "GET", "/version", "version"
	default:
		return fmt.Errorf("%s: unknown subcommand %q", group, action)
	}
	var input, contentFile string
	if len(schema) > 0 {
		fs.StringVar(&input, "input", "", "JSON request file; explicit flags override its fields")
		registerFields(fs, schema)
	}
	if group == "entry" && (action == "update" || action == "import") {
		fs.StringVar(&contentFile, "content-file", "", "UTF-8 article content file (HTML accepted)")
	}
	full := false
	if response != "receipt" {
		fs.BoolVar(&full, "full", false, "return the raw API object")
	}
	all := false
	if group == "feed" && action == "refresh" {
		fs.BoolVar(&all, "all", false, "queue refresh of all feeds")
	}
	if err := parseFlags(fs, args[1:]); err != nil {
		return err
	}
	if group == "feed" && action == "refresh" {
		needsID = !all
		if all {
			path = "/feeds/refresh"
		}
	}
	var id int64
	if needsID {
		var err error
		id, err = oneID(fs)
		if err != nil {
			return err
		}
		path = fmt.Sprintf(path, id)
	} else if err := noArgs(fs); err != nil {
		return err
	}
	body, err := readRequestFields(fs, schema, input)
	if err != nil {
		return err
	}
	if contentFile != "" {
		if _, ok := body["content"]; ok {
			return errors.New("use either content or --content-file, not both")
		}
		content, err := readInputFile(contentFile)
		if err != nil {
			return errors.New("cannot read content file")
		}
		body["content"] = string(content)
	}
	if err := validateUserRequest(group, action, body); err != nil {
		return err
	}
	client, err := newMinifluxClient()
	if err != nil {
		return err
	}
	if group == "entry" && action == "mark-all-read" {
		data, err := client.request("GET", "/me", nil, nil)
		if err != nil {
			return err
		}
		id, err = responseID(data, "id")
		if err != nil {
			return err
		}
		path = fmt.Sprintf(path, id)
	}
	var payload any
	if len(schema) > 0 {
		payload = body
	}
	data, err := client.request(method, path, nil, payload)
	if err != nil {
		return err
	}
	if response == "receipt" {
		receipt := map[string]any{"action": group + " " + action, "accepted": true}
		if id > 0 {
			receipt["id"] = id
		}
		if all {
			receipt["all"] = true
		}
		return writeJSON(receipt)
	}
	return writeUserResponse(response, data, full)
}

func validateUserRequest(group, action string, body map[string]any) error {
	require := func(key string) error {
		value, ok := body[key].(string)
		if !ok || strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s %s: %s is required and must not be empty", group, action, flagName(key))
		}
		return nil
	}
	if action == "update" && len(body) == 0 {
		return errors.New("update requires at least one field")
	}
	if group == "feed" && action == "create" {
		if err := require("feed_url"); err != nil {
			return err
		}
	}
	if (group == "feed" && action == "discover") || (group == "entry" && action == "import") {
		if err := require("url"); err != nil {
			return err
		}
	}
	if group == "category" && action == "create" {
		if err := require("title"); err != nil {
			return err
		}
	}
	for _, key := range []string{"title", "feed_url", "site_url", "description", "content"} {
		if value, exists := body[key]; exists && action == "update" && value == "" {
			return fmt.Errorf("%s cannot be empty: upstream would ignore it", flagName(key))
		}
	}
	if v, ok := body["category_id"].(int64); ok && v <= 0 {
		return errors.New("category must be positive")
	}
	if v, ok := body["media_progression"].(int64); ok && v < 0 {
		return errors.New("media-progression must be nonnegative")
	}
	if v, ok := body["published_at"].(int64); ok && v <= 0 {
		return errors.New("published-at must be a positive Unix timestamp")
	}
	if v, ok := body["status"].(string); ok && v != "read" && v != "unread" {
		return errors.New("status must be read or unread")
	}
	return nil
}

func responseID(data json.RawMessage, key string) (int64, error) {
	var obj map[string]json.RawMessage
	var id int64
	if json.Unmarshal(data, &obj) != nil || json.Unmarshal(obj[key], &id) != nil || id <= 0 {
		return 0, errors.New("decoding miniflux response: missing or invalid " + key)
	}
	return id, nil
}

func writeUserResponse(kind string, data json.RawMessage, full bool) error {
	if len(data) == 0 {
		return errors.New("decoding miniflux response: empty JSON response")
	}
	if full {
		return writeJSON(data)
	}
	switch kind {
	case "feed":
		value, err := decodeFeed(data)
		if err != nil {
			return err
		}
		return writeJSON(value)
	case "entry":
		value, err := decodeEntry(data, "text")
		if err != nil {
			return err
		}
		return writeJSON(value)
	case "id", "feed_id":
		id, err := responseID(data, kind)
		if err != nil {
			return err
		}
		return writeJSON(map[string]int64{kind: id})
	case "category":
		id, err := responseID(data, "id")
		if err != nil {
			return err
		}
		var obj struct {
			Title        string `json:"title"`
			HideGlobally bool   `json:"hide_globally"`
		}
		if err := json.Unmarshal(data, &obj); err != nil {
			return errors.New("decoding miniflux category")
		}
		return writeJSON(map[string]any{"id": id, "title": obj.Title, "hide_globally": obj.HideGlobally})
	case "discovery":
		var subscriptions []struct {
			Title string `json:"title"`
			URL   string `json:"url"`
			Type  string `json:"type"`
		}
		if json.Unmarshal(data, &subscriptions) != nil || subscriptions == nil {
			return errors.New("decoding miniflux subscriptions")
		}
		for _, s := range subscriptions {
			if s.URL == "" {
				return errors.New("decoding miniflux subscription: missing URL")
			}
		}
		return writeJSON(subscriptions)
	case "icon", "enclosure":
		if _, err := responseID(data, "id"); err != nil {
			return err
		}
		if kind == "icon" {
			var icon struct {
				ID       int64   `json:"id"`
				MimeType string  `json:"mime_type"`
				Data     *string `json:"data"`
			}
			if json.Unmarshal(data, &icon) != nil || icon.Data == nil || icon.MimeType == "" {
				return errors.New("decoding miniflux icon")
			}
			return writeJSON(icon)
		}
		var enclosure struct {
			ID               int64  `json:"id"`
			EntryID          int64  `json:"entry_id"`
			URL              string `json:"url"`
			MimeType         string `json:"mime_type"`
			Size             int64  `json:"size"`
			MediaProgression int64  `json:"media_progression"`
		}
		if json.Unmarshal(data, &enclosure) != nil || enclosure.EntryID <= 0 {
			return errors.New("decoding miniflux enclosure")
		}
		return writeJSON(enclosure)
	case "integration":
		var status struct {
			HasIntegrations *bool `json:"has_integrations"`
		}
		if json.Unmarshal(data, &status) != nil || status.HasIntegrations == nil {
			return errors.New("decoding miniflux integration status")
		}
		return writeJSON(status)
	case "version":
		var version struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(data, &version) != nil || version.Version == "" {
			return errors.New("decoding miniflux server version")
		}
		return writeJSON(data)
	}
	return errors.New("unsupported response shape")
}

func runEntryIDs(args []string) error {
	fs := newFlagSet("entry ids")
	status := fs.String("status", "all", "read, unread or all")
	starred := fs.Bool("starred", false, "filter starred state; =false includes only unstarred")
	limit := fs.Int("limit", 1000, "page size, 1..10000")
	offset := fs.Int("offset", 0, "offset")
	full := fs.Bool("full", false, "raw response")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if err := noArgs(fs); err != nil {
		return err
	}
	if *status != "all" && *status != "read" && *status != "unread" {
		return errors.New("status must be all, read or unread")
	}
	if *limit < 1 || *limit > 10000 || *offset < 0 {
		return errors.New("limit must be 1..10000 and offset nonnegative")
	}
	query := url.Values{"limit": {strconv.Itoa(*limit)}, "offset": {strconv.Itoa(*offset)}}
	if *status != "all" {
		query.Set("status", *status)
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "starred" {
			query.Set("starred", strconv.FormatBool(*starred))
		}
	})
	client, err := newMinifluxClient()
	if err != nil {
		return err
	}
	data, err := client.request("GET", "/entries/ids", query, nil)
	if err != nil {
		return err
	}
	if !*full {
		var result struct {
			Total *int    `json:"total"`
			IDs   []int64 `json:"entry_ids"`
		}
		if json.Unmarshal(data, &result) != nil || result.Total == nil || *result.Total < 0 || result.IDs == nil {
			return errors.New("decoding miniflux entry IDs")
		}
		for _, id := range result.IDs {
			if id <= 0 {
				return errors.New("decoding miniflux entry IDs: invalid ID")
			}
		}
	}
	return writeJSON(data)
}
