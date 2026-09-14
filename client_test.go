package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
