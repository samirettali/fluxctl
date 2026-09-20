package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// Runner tests intentionally remain serial: they replace process-wide environment
// variables and stdout, while exercising the same client construction as the CLI.
type runnerExchange struct {
	method   string
	path     string
	query    string
	body     string
	status   int
	response string
}

func runnerServer(t *testing.T, exchanges ...runnerExchange) {
	t.Helper()
	var mu sync.Mutex
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		index := calls
		calls++
		if index >= len(exchanges) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected request", http.StatusInternalServerError)
			return
		}
		want := exchanges[index]
		if r.Method != want.method || r.URL.Path != "/v1"+want.path || r.URL.RawQuery != want.query {
			t.Errorf("request %d = %s %s, want %s /v1%s?%s", index, r.Method, r.URL, want.method, want.path, want.query)
		}
		if got := r.Header.Get("X-Auth-Token"); got != "runner-test-key" {
			t.Errorf("X-Auth-Token = %q, want dummy key", got)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q, want application/json", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading request: %v", err)
		} else if want.body == "" {
			if len(body) != 0 {
				t.Errorf("unexpected request body: %s", body)
			}
		} else {
			if got := r.Header.Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", got)
			}
			checkRunnerJSON(t, string(body), want.body)
		}
		w.Header().Set("Content-Type", "application/json")
		status := want.status
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		if want.response != "" {
			if _, err := io.WriteString(w, want.response); err != nil {
				t.Errorf("writing response: %v", err)
			}
		}
	}))
	t.Cleanup(func() {
		server.Close()
		mu.Lock()
		defer mu.Unlock()
		if calls != len(exchanges) {
			t.Errorf("got %d requests, want %d", calls, len(exchanges))
		}
	})
	t.Setenv("MINIFLUX_URL", server.URL)
	t.Setenv("MINIFLUX_API_KEY", "runner-test-key")
}

func captureRunner(t *testing.T, args ...string) (string, error) {
	t.Helper()
	// A file avoids a pipe buffer deadlock if a regression produces large output.
	output, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	previous := os.Stdout
	os.Stdout = output
	defer func() { os.Stdout = previous }()
	runErr := run(args)
	if _, err := output.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(output)
	if err != nil {
		t.Fatal(err)
	}
	return string(data), runErr
}

func checkRunnerJSON(t *testing.T, got, want string) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Errorf("invalid expected JSON: %v", err)
		return
	}
	if err := json.Unmarshal([]byte(got), &gotValue); err != nil {
		t.Errorf("invalid output JSON %q: %v", got, err)
		return
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Errorf("JSON = %s\nwant = %s", got, want)
	}
}

func checkRunnerSuccess(t *testing.T, want string, args ...string) {
	t.Helper()
	got, err := captureRunner(t, args...)
	if err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	checkRunnerJSON(t, got, want)
}

const runnerRawFeed = `{
	"id":7,"title":"Systems","site_url":"https://example.com","feed_url":"https://example.com/rss",
	"checked_at":"2026-09-14T12:00:00Z","disabled":false,
	"parsing_error_count":2,"parsing_error_message":"timeout","user_id":99,
	"category":{"id":3,"title":"Tech","user_id":99,"hide_globally":false}
}`

const runnerTrimmedFeed = `{
	"id":7,"title":"Systems","site_url":"https://example.com","feed_url":"https://example.com/rss",
	"checked_at":"2026-09-14T12:00:00Z","disabled":false,
	"parsing_error":"2 consecutive failures: timeout","category":{"id":3,"title":"Tech"}
}`

const runnerRawEntry = `{
	"id":42,"feed_id":7,"title":"Queues","url":"https://example.com/queues","author":"Ada",
	"published_at":"2026-09-13T10:00:00Z","created_at":"2026-09-14T11:00:00Z",
	"status":"unread","starred":true,"reading_time":4,"content":"<p>Hello &amp; <b>world</b>.</p>",
	"feed":` + runnerRawFeed + `,
	"enclosures":[{"url":"https://example.com/audio.mp3","mime_type":"audio/mpeg","size":123,"id":8}],
	"tags":["go"],"user_id":99
}`

// Keep expected wire output independent of the production trimming helpers.
const runnerTrimmedEntryFields = `
	"id":42,"title":"Queues","url":"https://example.com/queues","author":"Ada",
	"published_at":"2026-09-13T10:00:00Z","created_at":"2026-09-14T11:00:00Z",
	"status":"unread","starred":true,"reading_time":4,
	"feed":{"id":7,"title":"Systems"},"category":{"id":3,"title":"Tech"},
	"enclosures":[{"url":"https://example.com/audio.mp3","mime_type":"audio/mpeg"}],"tags":["go"]
`

func TestRunEntryList(t *testing.T) {
	const defaultQuery = "direction=desc&limit=50&order=published_at&status=unread"
	const raw = `{"total":12,"entries":[` + runnerRawEntry + `],"server_extra":true}`
	cases := []struct {
		name     string
		args     []string
		query    string
		response string
		want     string
	}{
		{"defaults", nil, defaultQuery, raw, `{"total":12,"entries":[{` + runnerTrimmedEntryFields + `}]}`},
		{"content", []string{"--content"}, defaultQuery, raw, `{"total":12,"entries":[{` + runnerTrimmedEntryFields + `,"content":"Hello & world."}]}`},
		{"full", []string{"--full"}, defaultQuery, raw, raw},
		{"full overrides content", []string{"--content", "--full"}, defaultQuery, raw, raw},
		{"empty", nil, defaultQuery, `{"total":0,"entries":[]}`, `{"total":0,"entries":[]}`},
		{
			"filters on wire",
			[]string{"--status", "read", "--starred", "--feed", "7", "--category", "3", "--search", "queues & logs",
				"--limit", "20", "--offset", "40", "--order", "created_at", "--direction", "asc",
				"--since", "2026-09-13T00:00:00Z", "--until", "2026-09-14T00:00:00Z",
				"--published-since", "2026-09-11T00:00:00Z", "--published-until", "2026-09-12T00:00:00Z"},
			"category_id=3&changed_after=1789257600&changed_before=1789344000&direction=asc&feed_id=7&limit=20&offset=40&order=created_at&published_after=1789084800&published_before=1789171200&search=queues+%26+logs&starred=true&status=read",
			raw, `{"total":12,"entries":[{` + runnerTrimmedEntryFields + `}]}`,
		},
		{"all without cap", []string{"--status", "all", "--limit", "0"}, "direction=desc&limit=0&order=published_at", raw, `{"total":12,"entries":[{` + runnerTrimmedEntryFields + `}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runnerServer(t, runnerExchange{method: "GET", path: "/entries", query: tc.query, response: tc.response})
			checkRunnerSuccess(t, tc.want, append([]string{"entry", "list"}, tc.args...)...)
		})
	}
}

func TestRunEntryGet(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"text", []string{"42"}, `{` + runnerTrimmedEntryFields + `,"content":"Hello & world."}`},
		{"html after ID", []string{"42", "--html"}, `{` + runnerTrimmedEntryFields + `,"content":"<p>Hello &amp; <b>world</b>.</p>"}`},
		{"full before ID", []string{"--full", "42"}, runnerRawEntry},
		{"full overrides html", []string{"42", "--full", "--html"}, runnerRawEntry},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runnerServer(t, runnerExchange{method: "GET", path: "/entries/42", response: runnerRawEntry})
			checkRunnerSuccess(t, tc.want, append([]string{"entry", "get"}, tc.args...)...)
		})
	}
}

func TestRunEntryFetch(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"text", []string{"42"}, `{"id":42,"content":"Hello & world.","reading_time":8}`},
		{"html", []string{"42", "--html"}, `{"id":42,"content":"<p>Hello &amp; <b>world</b>.</p>","reading_time":8}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// No update_content parameter: fetching must not mutate the stored entry.
			runnerServer(t, runnerExchange{method: "GET", path: "/entries/42/fetch-content", response: `{"content":"<p>Hello &amp; <b>world</b>.</p>","reading_time":8,"server_extra":true}`})
			checkRunnerSuccess(t, tc.want, append([]string{"entry", "fetch"}, tc.args...)...)
		})
	}
}

func TestRunEntryStatus(t *testing.T) {
	for _, status := range []string{"read", "unread"} {
		t.Run(status, func(t *testing.T) {
			runnerServer(t, runnerExchange{
				method: "PUT", path: "/entries", status: http.StatusNoContent,
				body: fmt.Sprintf(`{"entry_ids":[42,43,999],"status":%q}`, status),
			})
			checkRunnerSuccess(t, fmt.Sprintf(`{"entries":[42,43,999],"status":%q}`, status), "entry", status, "42", "43", "999")
		})
	}
}

func TestRunEntrySave(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		exchanges []runnerExchange
		want      string
		authError bool
	}{
		{
			name: "accepted", args: []string{"42", "43"},
			exchanges: []runnerExchange{
				{method: "POST", path: "/entries/42/save", status: http.StatusAccepted},
				{method: "POST", path: "/entries/43/save", status: http.StatusAccepted},
			},
			want: `{"saved":[42,43],"failed":[]}`,
		},
		{
			name: "continues after per-entry failures", args: []string{"42", "43", "44", "45"},
			exchanges: []runnerExchange{
				{method: "POST", path: "/entries/42/save", status: http.StatusAccepted},
				{method: "POST", path: "/entries/43/save", status: http.StatusNotFound, response: `{"error_message":"entry not found"}`},
				{method: "POST", path: "/entries/44/save", status: http.StatusInternalServerError, response: `{"error_message":"integration unavailable"}`},
				{method: "POST", path: "/entries/45/save", status: http.StatusAccepted},
			},
			want: `{"saved":[42,45],"failed":[{"id":43,"error":"miniflux request failed (status 404): entry not found"},{"id":44,"error":"miniflux request failed (status 500): integration unavailable"}]}`,
		},
		{
			name: "all failed", args: []string{"42"},
			exchanges: []runnerExchange{{method: "POST", path: "/entries/42/save", status: http.StatusNotFound, response: `{"error_message":"entry not found"}`}},
			want:      `{"saved":[],"failed":[{"id":42,"error":"miniflux request failed (status 404): entry not found"}]}`,
		},
		{
			name: "401 stops immediately", args: []string{"42", "43"}, authError: true,
			exchanges: []runnerExchange{{method: "POST", path: "/entries/42/save", status: http.StatusUnauthorized, response: `{"error_message":"unauthorized"}`}},
		},
		{
			name: "401 discards partial output", args: []string{"42", "43", "44"}, authError: true,
			exchanges: []runnerExchange{
				{method: "POST", path: "/entries/42/save", status: http.StatusAccepted},
				{method: "POST", path: "/entries/43/save", status: http.StatusUnauthorized, response: `{"error_message":"unauthorized"}`},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runnerServer(t, tc.exchanges...)
			got, err := captureRunner(t, append([]string{"entry", "save"}, tc.args...)...)
			if tc.authError {
				checkRunnerAuthError(t, got, err)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			checkRunnerJSON(t, got, tc.want)
		})
	}
}

func checkRunnerAuthError(t *testing.T, output string, err error) {
	t.Helper()
	var authErr *authError
	var apiErr *APIError
	if !errors.As(err, &authErr) || authErr.Fix == "" {
		t.Errorf("error = %v, want authError with remedy", err)
	}
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnauthorized {
		t.Errorf("error = %v, want wrapped 401 APIError", err)
	}
	if output != "" {
		t.Errorf("error wrote stdout: %q", output)
	}
}

func TestRunEntryStar(t *testing.T) {
	for _, tc := range []struct {
		command string
		starred bool
	}{
		{"star", true}, {"unstar", false},
	} {
		t.Run(tc.command, func(t *testing.T) {
			runnerServer(t,
				runnerExchange{method: "GET", path: "/entries/42", response: fmt.Sprintf(`{"starred":%t}`, !tc.starred)},
				runnerExchange{method: "PUT", path: "/entries/42/bookmark", status: http.StatusNoContent},
				runnerExchange{method: "GET", path: "/entries/43", response: fmt.Sprintf(`{"starred":%t}`, tc.starred)},
			)
			checkRunnerSuccess(t, fmt.Sprintf(`{"starred":%t,"changed":[42],"skipped":[43],"failed":[]}`, tc.starred), "entry", tc.command, "42", "43")
		})
	}
	t.Run("auth error", func(t *testing.T) {
		runnerServer(t, runnerExchange{method: "GET", path: "/entries/42", status: http.StatusUnauthorized})
		output, err := captureRunner(t, "entry", "star", "42", "43")
		checkRunnerAuthError(t, output, err)
	})
}

func TestRunFeed(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		path     string
		response string
		want     string
	}{
		{"list trimmed", []string{"list"}, "/feeds", `[` + runnerRawFeed + `]`, `[` + runnerTrimmedFeed + `]`},
		{"list full", []string{"list", "--full"}, "/feeds", `[` + runnerRawFeed + `]`, `[` + runnerRawFeed + `]`},
		{"category trimmed", []string{"list", "--category", "3"}, "/categories/3/feeds", `[` + runnerRawFeed + `]`, `[` + runnerTrimmedFeed + `]`},
		{"category full", []string{"list", "--full", "--category", "3"}, "/categories/3/feeds", `[` + runnerRawFeed + `]`, `[` + runnerRawFeed + `]`},
		{"empty list", []string{"list"}, "/feeds", `[]`, `[]`},
		{"get trimmed", []string{"get", "7"}, "/feeds/7", runnerRawFeed, runnerTrimmedFeed},
		{"get full after ID", []string{"get", "7", "--full"}, "/feeds/7", runnerRawFeed, runnerRawFeed},
		{"counters", []string{"counters"}, "/feeds/counters", `{"reads":{"7":12},"unreads":{"7":3}}`, `{"reads":{"7":12},"unreads":{"7":3}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runnerServer(t, runnerExchange{method: "GET", path: tc.path, response: tc.response})
			checkRunnerSuccess(t, tc.want, append([]string{"feed"}, tc.args...)...)
		})
	}
}

func TestRunCategory(t *testing.T) {
	const raw = `[{"id":3,"title":"Tech","feed_count":0,"total_unread":0,"user_id":99,"hide_globally":true},{"id":4,"title":"News"}]`
	for _, tc := range []struct {
		name     string
		args     []string
		response string
		want     string
	}{
		{"trimmed preserves zero counts", []string{"list"}, raw, `[{"id":3,"title":"Tech","feed_count":0,"total_unread":0},{"id":4,"title":"News"}]`},
		{"full", []string{"list", "--full"}, raw, raw},
		{"empty", []string{"list"}, `[]`, `[]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runnerServer(t, runnerExchange{method: "GET", path: "/categories", query: "counts=true", response: tc.response})
			checkRunnerSuccess(t, tc.want, append([]string{"category"}, tc.args...)...)
		})
	}
}

func TestRunMe(t *testing.T) {
	// me, like feed counters, always passes through Miniflux's object.
	const raw = `{"id":99,"username":"tester","is_admin":true,"timezone":"Europe/Rome","theme":"system"}`
	runnerServer(t, runnerExchange{method: "GET", path: "/me", response: raw})
	checkRunnerSuccess(t, raw, "me")
}

func TestRunnerResponseErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		path string
		what string
	}{
		{"me", []string{"me"}, "/me", ""},
		{"category list", []string{"category", "list"}, "/categories", "categories"},
		{"feed list", []string{"feed", "list"}, "/feeds", "feeds"},
		{"feed get", []string{"feed", "get", "7"}, "/feeds/7", "feed"},
		{"feed counters", []string{"feed", "counters"}, "/feeds/counters", ""},
		{"entry list", []string{"entry", "list"}, "/entries", "entries"},
		{"entry get", []string{"entry", "get", "42"}, "/entries/42", "entry"},
		{"entry fetch", []string{"entry", "fetch", "42"}, "/entries/42/fetch-content", "fetched content"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := ""
			if tc.path == "/categories" {
				query = "counts=true"
			} else if tc.path == "/entries" {
				query = "direction=desc&limit=50&order=published_at&status=unread"
			}
			t.Run("API error", func(t *testing.T) {
				runnerServer(t, runnerExchange{method: "GET", path: tc.path, query: query, status: http.StatusServiceUnavailable, response: `{"error_message":"try later"}`})
				output, err := captureRunner(t, tc.args...)
				var apiErr *APIError
				if !errors.As(err, &apiErr) || apiErr.Status != http.StatusServiceUnavailable {
					t.Errorf("error = %v, want 503 APIError", err)
				}
				if output != "" {
					t.Errorf("error wrote stdout: %q", output)
				}
			})
			if tc.what != "" {
				t.Run("malformed JSON", func(t *testing.T) {
					runnerServer(t, runnerExchange{method: "GET", path: tc.path, query: query, response: `{"broken":`})
					output, err := captureRunner(t, tc.args...)
					// Malformed JSON may be rejected by the client before it
					// reaches the shape decoder; both must fail without output.
					var syntaxErr *json.SyntaxError
					shapeError := errors.As(err, &syntaxErr) && strings.Contains(err.Error(), "decoding "+tc.what+":")
					clientError := err != nil && err.Error() == "decoding miniflux response: invalid JSON"
					if !shapeError && !clientError {
						t.Errorf("error = %v, want invalid JSON error from client or %s decoder", err, tc.what)
					}
					if output != "" {
						t.Errorf("error wrote stdout: %q", output)
					}
				})
			}
		})
	}
	for _, status := range []string{"read", "unread"} {
		t.Run(status, func(t *testing.T) {
			runnerServer(t, runnerExchange{method: "PUT", path: "/entries", body: fmt.Sprintf(`{"entry_ids":[42],"status":%q}`, status), status: http.StatusUnauthorized})
			output, err := captureRunner(t, "entry", status, "42")
			checkRunnerAuthError(t, output, err)
		})
	}
}

func TestDecodeSingleObjectErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		decode func(json.RawMessage) error
	}{
		{"feed", func(data json.RawMessage) error { _, err := decodeFeed(data); return err }},
		{"entry", func(data json.RawMessage) error { _, err := decodeEntry(data, "text"); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, data := range []string{`{"id":`, `{"id":"not a number"}`} {
				err := tc.decode(json.RawMessage(data))
				if err == nil || !strings.HasPrefix(err.Error(), "decoding "+tc.name+":") {
					t.Fatalf("decode(%q) = %v, want contextual error", data, err)
				}
				var syntaxErr *json.SyntaxError
				var typeErr *json.UnmarshalTypeError
				if !errors.As(err, &syntaxErr) && !errors.As(err, &typeErr) {
					t.Errorf("decode(%q) lost underlying JSON error: %v", data, err)
				}
			}
		})
	}
}
