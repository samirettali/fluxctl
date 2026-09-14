package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testClient(t *testing.T, handler http.HandlerFunc) *minifluxClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &minifluxClient{baseURL: server.URL, apiKey: "secret", http: server.Client()}
}

func TestRequestSendsTokenAndDecodesEnvelope(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Auth-Token") != "secret" {
			t.Errorf("missing token header")
		}
		if r.URL.Path != "/v1/entries" || r.URL.Query().Get("status") != "unread" {
			t.Errorf("unexpected request %s", r.URL)
		}
		w.Write([]byte(`{"total":1,"entries":[{"id":1,"title":"T","content":"<p>Hi</p>","feed":{"id":2,"title":"F","category":{"id":3,"title":"C"}}}]}`))
	})
	data, err := client.request("GET", "/entries", map[string][]string{"status": {"unread"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	list, err := decodeEntries(data, "text")
	if err != nil {
		t.Fatal(err)
	}
	if list.Total != 1 || list.Entries[0].Category.Title != "C" || *list.Entries[0].Content != "Hi" {
		t.Errorf("unexpected decode: %+v", list)
	}
	plain, _ := decodeEntries(data, "")
	if plain.Entries[0].Content != nil {
		t.Error("content should be omitted by default")
	}
}

func TestRequestErrors(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/unauthorized":
			w.WriteHeader(401)
			w.Write([]byte(`{"error_message":"access unauthorized"}`))
		case "/v1/missing":
			w.WriteHeader(404)
			w.Write([]byte(`{"error_message":"resource not found"}`))
		case "/v1/empty":
			w.WriteHeader(204)
		}
	})
	_, err := client.request("GET", "/unauthorized", nil, nil)
	var authErr *authError
	if !errors.As(err, &authErr) || authErr.Fix == "" {
		t.Errorf("401 should be an authError with a fix, got %v", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 401 {
		t.Errorf("authError should unwrap to the APIError, got %v", err)
	}
	_, err = client.request("GET", "/missing", nil, nil)
	if !errors.As(err, &apiErr) || apiErr.Status != 404 || apiErr.Details != "resource not found" {
		t.Errorf("unexpected 404 handling: %v", err)
	}
	data, err := client.request("PUT", "/empty", nil, map[string]any{"a": 1})
	if err != nil || data != nil {
		t.Errorf("204 should be a nil success, got %v %v", data, err)
	}
}

func TestNewClientRejectsBadURL(t *testing.T) {
	t.Setenv("MINIFLUX_API_KEY", "k")
	for _, bad := range []string{"localhost:8080", "http://", "ftp://x", "rss.example.com"} {
		t.Setenv("MINIFLUX_URL", bad)
		_, err := newMinifluxClient()
		var authErr *authError
		if !errors.As(err, &authErr) {
			t.Errorf("%q should be rejected with a fix, got %v", bad, err)
		}
	}
	t.Setenv("MINIFLUX_URL", "https://rss.example.com/")
	client, err := newMinifluxClient()
	if err != nil || client.baseURL != "https://rss.example.com" {
		t.Errorf("valid URL rejected or not trimmed: %v %v", client, err)
	}
}

func TestDecodeAPIErrorNonJSON(t *testing.T) {
	var apiErr *APIError
	err := decodeAPIError(502, []byte("<html>Bad Gateway</html>"))
	if !errors.As(err, &apiErr) || apiErr.Status != 502 || apiErr.Details != "<html>Bad Gateway</html>" {
		t.Errorf("non-JSON body should land in details verbatim: %v", err)
	}
	if err := decodeAPIError(500, nil); !errors.As(err, &apiErr) || apiErr.Details != "" {
		t.Errorf("empty body should leave details empty: %v", err)
	}
}

func TestStarIsIdempotent(t *testing.T) {
	toggles := 0
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/entries/1":
			w.Write([]byte(`{"id":1,"starred":true}`))
		case r.Method == "GET" && r.URL.Path == "/v1/entries/2":
			w.Write([]byte(`{"id":2,"starred":false}`))
		case r.Method == "PUT" && r.URL.Path == "/v1/entries/2/bookmark":
			toggles++
			w.WriteHeader(204)
		case r.Method == "PUT" && r.URL.Path == "/v1/entries/1/bookmark":
			t.Error("already starred entry must not be toggled")
		default:
			w.WriteHeader(404)
			w.Write([]byte(`{"error_message":"resource not found"}`))
		}
	})
	result, err := starEntries(client, true, []int64{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	if toggles != 1 || len(result.Changed) != 1 || result.Changed[0] != 2 {
		t.Errorf("expected exactly entry 2 toggled, got %+v (toggles=%d)", result, toggles)
	}
	if len(result.Skipped) != 1 || result.Skipped[0] != 1 {
		t.Errorf("entry 1 should be skipped: %+v", result)
	}
	if len(result.Failed) != 1 || result.Failed[0].ID != 3 {
		t.Errorf("entry 3 should fail without aborting: %+v", result)
	}
}

func TestStarFailsFastOnAuthError(t *testing.T) {
	calls := 0
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(401)
		w.Write([]byte(`{"error_message":"access unauthorized"}`))
	})
	_, err := starEntries(client, true, []int64{1, 2, 3})
	var authErr *authError
	if !errors.As(err, &authErr) {
		t.Fatalf("expected an authError, got %v", err)
	}
	if calls != 1 {
		t.Errorf("should stop at the first 401, made %d calls", calls)
	}
}

func TestDecodeAPIErrorTruncatesLongBodies(t *testing.T) {
	var apiErr *APIError
	err := decodeAPIError(502, []byte(strings.Repeat("x", 1000)))
	if !errors.As(err, &apiErr) || len([]rune(apiErr.Details)) != maxErrorDetails+1 {
		t.Errorf("details should be truncated to %d chars plus an ellipsis, got %d", maxErrorDetails, len(apiErr.Details))
	}
}

func TestTrimEntryEnclosures(t *testing.T) {
	data := []byte(`{"total":1,"entries":[{"id":1,"feed":{"category":{}},"enclosures":[{"url":"https://x/v.mp4","mime_type":"video/mp4","size":3}]}]}`)
	list, err := decodeEntries(data, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Entries[0].Enclosures) != 1 || list.Entries[0].Enclosures[0].URL != "https://x/v.mp4" {
		t.Errorf("enclosures not trimmed through: %+v", list.Entries[0])
	}
}

func TestTrimFeedParsingError(t *testing.T) {
	var raw rawFeed
	_ = json.Unmarshal([]byte(`{"id":1,"title":"F","parsing_error_count":3,"parsing_error_message":"boom","category":{"id":2,"title":"C"}}`), &raw)
	f := trimFeed(raw)
	if f.ParsingError != "3 consecutive failures: boom" {
		t.Errorf("parsing_error = %q", f.ParsingError)
	}
	raw.ParsingErrorCount = 0
	if trimFeed(raw).ParsingError != "" {
		t.Error("healthy feed should omit parsing_error")
	}
}
