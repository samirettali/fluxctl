package main

import (
	"bytes"
	"encoding/json"
	"net/url"
	"sort"
	"strings"
)

const redacted = "[redacted]"

// API responses can contain subscription credentials even on read-only routes
// (including feeds nested inside entries). Sanitize before trimming or --full.
func redactResponse(data json.RawMessage, sources ...any) json.RawMessage {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return data
	}
	var secrets []string
	seen := map[string]bool{}
	addSecret := func(values ...string) {
		for _, value := range values {
			if value != "" && !seen[value] {
				secrets = append(secrets, value)
				seen[value] = true
			}
		}
	}
	var collect func(any)
	collect = func(v any) {
		switch obj := v.(type) {
		case map[string]any:
			for key, child := range obj {
				if s, ok := child.(string); ok {
					if key == "password" || key == "cookie" || key == "apprise_service_urls" || key == "webhook_url" {
						addSecret(s)
					}
					if u, err := url.Parse(s); err == nil {
						if u.User != nil {
							if password, ok := u.User.Password(); ok && password != "" {
								addSecret(password, url.QueryEscape(password), url.PathEscape(password))
							}
						}
						if key == "proxy_url" {
							if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
								addSecret(s)
							}
							for _, values := range u.Query() {
								for _, value := range values {
									addSecret(value)
								}
							}
						}
					} else if key == "proxy_url" {
						addSecret(s)
					}
				}
				collect(child)
			}
		case []any:
			for _, child := range obj {
				collect(child)
			}
		}
	}
	collect(value)
	for _, source := range sources {
		collect(source)
	}
	// Feed objects repeat per entry; deduplicate before building a single-pass
	// replacer so --limit 0 does not do quadratic work on repeated credentials.
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	pairs := make([]string, 0, len(secrets)*2)
	for _, secret := range secrets {
		pairs = append(pairs, secret, redacted)
	}
	replacer := strings.NewReplacer(pairs...)
	var clean func(any) any
	clean = func(v any) any {
		switch obj := v.(type) {
		case map[string]any:
			for key, child := range obj {
				if key == "password" || key == "cookie" || key == "apprise_service_urls" || key == "webhook_url" {
					if child != nil && child != "" {
						obj[key] = redacted
						continue
					}
				}
				if key == "proxy_url" {
					if s, ok := child.(string); ok && s != "" {
						u, err := url.Parse(s)
						if err != nil || u.Scheme == "" || u.Host == "" {
							obj[key] = redacted
							continue
						}
						u.User, u.RawQuery, u.Fragment = nil, "", ""
						child = u.String()
					}
				}
				obj[key] = clean(child)
			}
		case []any:
			for i, child := range obj {
				obj[i] = clean(child)
			}
		case string:
			if u, err := url.Parse(obj); err == nil && u.Scheme != "" && u.Host != "" && u.User != nil {
				u.User = nil
				obj = u.String()
			}
			return replacer.Replace(obj)
		}
		return v
	}
	encoded, err := json.Marshal(clean(value))
	if err != nil {
		return data
	}
	return encoded
}

func sensitiveRequest(method, path string) bool {
	return path == "/discover" || path == "/import" || path == "/export" ||
		((method == "POST" || method == "PUT") && (path == "/feeds" || strings.HasPrefix(path, "/feeds/")))
}
