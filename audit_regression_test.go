package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func auditConfiguredClient(t *testing.T, baseURL string) *minifluxClient {
	t.Helper()
	t.Setenv("MINIFLUX_URL", baseURL)
	t.Setenv("MINIFLUX_API_KEY", "audit-dummy-token")
	client, err := newMinifluxClient()
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestAuditRedirectDoesNotLeakToken(t *testing.T) {
	calls := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("X-Auth-Token") != "" {
			t.Error("API key leaked across origins")
		}
		fmt.Fprint(w, `{}`)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/stolen", http.StatusFound)
	}))
	defer source.Close()
	client := auditConfiguredClient(t, source.URL)
	if _, err := client.request("GET", "/me", nil, nil); err == nil {
		t.Error("cross-origin redirect should fail")
	}
	if calls != 0 {
		t.Errorf("redirect target contacted %d times", calls)
	}
}

func TestAuditRedirectMustNotTurnMutationIntoRead(t *testing.T) {
	for _, status := range []int{301, 302, 303} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			redirected := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/entries/1/save" {
					http.Redirect(w, r, "/landing", status)
					return
				}
				redirected = true
				fmt.Fprint(w, `{}`)
			}))
			defer server.Close()
			client := auditConfiguredClient(t, server.URL)
			if _, err := client.request("POST", "/entries/1/save", nil, nil); err == nil {
				t.Error("rewritten mutation must not be reported successful")
			}
			if redirected {
				t.Error("followed method-changing redirect")
			}
		})
	}
}

func TestAuditSameOriginRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/me" {
			http.Redirect(w, r, "/actual", http.StatusFound)
			return
		}
		if r.Header.Get("X-Auth-Token") != "audit-dummy-token" {
			t.Error("same-origin redirect lost authentication")
		}
		fmt.Fprint(w, `{"id":1}`)
	}))
	defer server.Close()
	client := auditConfiguredClient(t, server.URL)
	if _, err := client.request("GET", "/me", nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestAuditRedirectPolicy(t *testing.T) {
	original, _ := http.NewRequest("GET", "https://example.com/v1/me", nil)
	for _, target := range []string{"http://example.com/v1/me", "https://sub.example.com/v1/me", "https://example.com:444/v1/me", "https://user:audit-private-value@example.com/v1/me"} {
		req, _ := http.NewRequest("GET", target, nil)
		if err := checkRedirect(req, []*http.Request{original}); err == nil {
			t.Errorf("unsafe redirect accepted: %s", target)
		}
	}
	if err := checkRedirect(original, make([]*http.Request, 10)); err == nil {
		t.Error("redirect limit not enforced")
	}
}

func TestAuditRejectedRedirectDoesNotPrintCredentials(t *testing.T) {
	for _, location := range []string{
		"https://user:audit-private-value@example.com/v1/me",
		"https://example.com/v1/me?token=audit-private-value",
		"/%xx?token=audit-private-value",
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", location)
			w.WriteHeader(302)
		}))
		client := auditConfiguredClient(t, server.URL)
		_, err := client.request("GET", "/me", nil, nil)
		server.Close()
		if err == nil || strings.Contains(err.Error(), "audit-private-value") {
			t.Errorf("redirect credential error not sanitized: %v", err)
		}
	}
}

func TestAuditPreservingMutationRedirects(t *testing.T) {
	for _, status := range []int{307, 308} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				body, err := io.ReadAll(r.Body)
				if err != nil || r.Method != "PUT" || string(body) != `{"status":"read"}` || r.Header.Get("X-Auth-Token") != "audit-dummy-token" {
					t.Errorf("mutation changed: %s body=%s error=%v", r.Method, body, err)
				}
				if r.URL.Path == "/v1/entries" {
					http.Redirect(w, r, "/actual", status)
					return
				}
				w.WriteHeader(204)
			}))
			defer server.Close()
			client := auditConfiguredClient(t, server.URL)
			if _, err := client.request("PUT", "/entries", nil, map[string]string{"status": "read"}); err != nil {
				t.Fatal(err)
			}
			if calls != 2 {
				t.Errorf("got %d calls, want 2", calls)
			}
		})
	}
}

func TestAuditMalformedStarStateCannotMutate(t *testing.T) {
	for _, body := range []string{`null`, `{}`, `{"starred":null}`, `{"starred":"false"}`} {
		for _, star := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/%t", body, star), func(t *testing.T) {
				mutations := 0
				client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
					if r.Method != "GET" {
						mutations++
						w.WriteHeader(204)
						return
					}
					fmt.Fprint(w, body)
				})
				result, err := starEntries(client, star, []int64{1})
				if err != nil || mutations != 0 || len(result.Failed) != 1 || len(result.Skipped) != 0 {
					t.Errorf("malformed state must fail without changing/skipping: mutations=%d result=%+v error=%v", mutations, result, err)
				}
			})
		}
	}
}

func TestAuditMalformedSuccessResponse(t *testing.T) {
	for _, body := range []string{`<html>login</html>`, `{"ok":true} garbage`} {
		client := testClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) })
		if _, err := client.request("POST", "/entries/1/save", nil, nil); err == nil {
			t.Errorf("non-JSON success body accepted: %q", body)
		}
	}
}

func TestAuditEmptyGETResponseRejected(t *testing.T) {
	for _, body := range []string{"", " \n\t"} {
		client := testClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) })
		if _, err := client.request("GET", "/me", nil, nil); err == nil {
			t.Errorf("empty GET response accepted: %q", body)
		}
	}
}

func TestAuditMalformedTrimmedShapes(t *testing.T) {
	for _, body := range []string{`{}`, `{"id":0}`, `{"id":-1}`, `{"id":null}`} {
		if _, err := decodeEntry([]byte(body), ""); err == nil {
			t.Errorf("invalid entry accepted: %s", body)
		}
		if _, err := decodeFeed([]byte(body)); err == nil {
			t.Errorf("invalid feed accepted: %s", body)
		}
		if _, err := decodeFeeds([]byte("[" + body + "]")); err == nil {
			t.Errorf("invalid listed feed accepted: %s", body)
		}
		if _, err := decodeEntries([]byte(`{"total":1,"entries":[`+body+`]}`), ""); err == nil {
			t.Errorf("invalid listed entry accepted: %s", body)
		}
	}
	for _, body := range []string{`{}`, `{"total":0}`, `{"entries":[]}`, `{"total":null,"entries":[]}`, `{"total":-1,"entries":[]}`, `{"total":0,"entries":null}`} {
		if _, err := decodeEntries([]byte(body), ""); err == nil {
			t.Errorf("invalid entries envelope accepted: %s", body)
		}
	}
	for _, body := range []string{`null`, `[null]`} {
		if _, err := decodeFeeds([]byte(body)); err == nil {
			t.Errorf("invalid feed array accepted: %s", body)
		}
		if _, err := decodeCategories([]byte(body)); err == nil {
			t.Errorf("invalid category array accepted: %s", body)
		}
	}
}

func TestAuditEmptyCollectionsRemainValid(t *testing.T) {
	categories, err := decodeCategories([]byte(`[]`))
	if err != nil || categories == nil || len(categories) != 0 {
		t.Fatalf("empty categories: %v %v", categories, err)
	}
	feeds, err := decodeFeeds([]byte(`[]`))
	if err != nil || feeds == nil || len(feeds) != 0 {
		t.Fatalf("empty feeds: %v %v", feeds, err)
	}
	entries, err := decodeEntries([]byte(`{"total":0,"entries":[],"future_field":true}`), "")
	if err != nil || entries.Entries == nil || len(entries.Entries) != 0 {
		t.Fatalf("empty entries: %v %v", entries, err)
	}
}

func TestAuditFetchedContentFieldRequired(t *testing.T) {
	for _, body := range []string{`{}`, `{"content":null}`, `{"content":false}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
		auditConfiguredClient(t, server.URL)
		err := runEntryFetch([]string{"1"})
		server.Close()
		if err == nil {
			t.Errorf("missing/invalid fetched content accepted: %s", body)
		}
	}
}

func TestAuditNullObjectsRejected(t *testing.T) {
	data := json.RawMessage(`null`)
	if _, err := decodeEntry(data, ""); err == nil {
		t.Error("null decoded as an entry")
	}
	if _, err := decodeFeed(data); err == nil {
		t.Error("null decoded as a feed")
	}
	if _, err := decodeEntries(data, ""); err == nil {
		t.Error("null decoded as an entries envelope")
	}
	var fetched struct{ Content string }
	if err := decodeInto(data, &fetched, "fetched content"); err == nil {
		t.Error("null decoded as fetched content")
	}
}

func TestAuditFlagTerminator(t *testing.T) {
	for _, args := range [][]string{{"--", "--full"}, {"42", "--", "--full"}, {"--", "42", "--full"}, {"--", "--help"}} {
		fs := newFlagSet("audit")
		full := fs.Bool("full", false, "")
		err := parseFlags(fs, args)
		want := []string{}
		for _, arg := range args {
			if arg != "--" {
				want = append(want, arg)
			}
		}
		if err != nil || *full || !reflect.DeepEqual(fs.Args(), want) {
			t.Errorf("parseFlags(%q): full=%t args=%q error=%v; want positional %q", args, *full, fs.Args(), err, want)
		}
	}
}

func TestAuditDurationOverflow(t *testing.T) {
	for _, value := range []string{"106752d", "15251w", "9223372036854775807d", "9223372036854775807w"} {
		if d, err := parseDuration(value); err == nil {
			t.Errorf("overflowing duration %q accepted as %s", value, d)
		}
	}
	for _, value := range []string{"106751d", "15250w"} {
		if _, err := parseDuration(value); err != nil {
			t.Errorf("representable duration %q rejected: %v", value, err)
		}
	}
}

func TestAuditInvalidFiltersRejectedBeforeHTTP(t *testing.T) {
	for _, args := range [][]string{{"feed", "list", "--category", "-1"}, {"entry", "list", "--feed", "-1"}, {"entry", "list", "--category", "-1"}} {
		calls := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			fmt.Fprint(w, `null`)
		}))
		auditConfiguredClient(t, server.URL)
		err := run(args)
		server.Close()
		if err == nil || calls != 0 {
			t.Errorf("%v: error=%v calls=%d; invalid filters must fail before HTTP", args, err, calls)
		}
	}
}

func TestAuditEpochFilterNotSilentlyDropped(t *testing.T) {
	for _, value := range []string{"1970-01-01T00:00:00Z", "1969-12-31T23:59:59Z"} {
		o := entryListOptions{status: "all", order: "id", direction: "asc", until: value}
		q, err := o.query(time.Now())
		if err == nil && !q.Has("changed_before") {
			t.Errorf("explicit time filter %q silently omitted: %v", value, q)
		}
	}
}

func TestAuditBaseURLRejectsNonBaseComponents(t *testing.T) {
	t.Setenv("MINIFLUX_API_KEY", "audit-dummy-token")
	for _, value := range []string{"https://example.com?x=1", "https://example.com#fragment", "http://:8080", "https://user:audit-private-value@example.com"} {
		t.Setenv("MINIFLUX_URL", value)
		_, err := newMinifluxClient()
		var authErr *authError
		if !errors.As(err, &authErr) {
			t.Errorf("invalid base URL %q must fail with remedy: %v", value, err)
		}
		if err != nil && strings.Contains(err.Error(), "audit-private-value") {
			t.Error("URL credentials appeared in error")
		}
	}
}

func TestAuditFlagTerminatorAsValue(t *testing.T) {
	fs := newFlagSet("audit")
	search := fs.String("search", "", "")
	full := fs.Bool("full", false, "")
	if err := parseFlags(fs, []string{"42", "--search", "--", "--full", "--", "--help"}); err != nil {
		t.Fatal(err)
	}
	if *search != "--" || !*full || !reflect.DeepEqual(fs.Args(), []string{"42", "--help"}) {
		t.Fatalf("literal terminator value mishandled: search=%q full=%t args=%q", *search, *full, fs.Args())
	}
}

func TestAuditVersionRejectsArguments(t *testing.T) {
	for _, command := range []string{"version", "--version", "-v"} {
		if err := run([]string{command, "unexpected"}); err == nil {
			t.Errorf("%s accepted stray positional", command)
		}
	}
}

func TestAuditErrorStatusSurvivesBrokenBody(t *testing.T) {
	for _, status := range []int{401, 503} {
		client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "1000")
			w.WriteHeader(status)
			fmt.Fprint(w, "truncated")
		})
		_, err := client.request("GET", "/me", nil, nil)
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Status != status {
			t.Errorf("HTTP %d lost when body was truncated: %v", status, err)
		}
		if status == 401 && !isAuthError(err) {
			t.Errorf("401 lost authentication remedy: %v", err)
		}
	}
}

type auditRoundTripper func(*http.Request) (*http.Response, error)

func (f auditRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type auditCountingBody struct {
	remaining int
	read      int
	closed    bool
}

func (b *auditCountingBody) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		return 0, io.EOF
	}
	n := min(len(p), b.remaining)
	for i := range p[:n] {
		p[i] = 'x'
	}
	b.remaining -= n
	b.read += n
	return n, nil
}
func (b *auditCountingBody) Close() error { b.closed = true; return nil }

func TestAuditErrorBodyReadIsBounded(t *testing.T) {
	body := &auditCountingBody{remaining: 1024 * 1024}
	client := &minifluxClient{baseURL: "http://unused.invalid", http: &http.Client{Transport: auditRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 502, Body: body, Header: make(http.Header)}, nil
	})}}
	if _, err := client.request("GET", "/me", nil, nil); err == nil {
		t.Error("expected API failure")
	}
	if body.read > 16*1024 || !body.closed {
		t.Errorf("error body must be bounded and closed: read=%d closed=%t", body.read, body.closed)
	}
}

// This fake vault is the only executable used; it never touches the real rbw vault.
func TestAuditVaultCommandIsBounded(t *testing.T) {
	for _, stage := range []string{"unlocked", "get"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			script := "#!/bin/sh\nif [ \"$1\" != \"$AUDIT_HANG_STAGE\" ]; then exit 0; fi\nexec /bin/sleep 60\n"
			if err := os.WriteFile(filepath.Join(dir, "rbw"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
			t.Setenv("AUDIT_HANG_STAGE", stage)
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			start := time.Now()
			_, _, err := readVaultContext(ctx)
			if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
				t.Fatalf("vault command must return its timeout promptly: %v (%s)", err, time.Since(start))
			}
		})
	}
}

func TestAuditVaultFallback(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
if [ "$1" = unlocked ]; then exit 0; fi
if [ "$1" != get ] || [ "$2" != --raw ] || [ "$3" != miniflux-api-key ]; then exit 2; fi
printf '%s' '{"data":{"password":"dummy-vault-key","uris":[{"uri":"https://vault.example.com/rss/"}]}}'
`
	if err := os.WriteFile(filepath.Join(dir, "rbw"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	for _, tc := range []struct{ url, key, wantURL, wantKey string }{
		{"", "", "https://vault.example.com/rss", "dummy-vault-key"},
		{"https://env.example.com/prefix/", "", "https://env.example.com/prefix", "dummy-vault-key"},
		{"", "dummy-env-key", "https://vault.example.com/rss", "dummy-env-key"},
	} {
		t.Setenv("MINIFLUX_URL", tc.url)
		t.Setenv("MINIFLUX_API_KEY", tc.key)
		client, err := newMinifluxClient()
		if err != nil {
			t.Fatal(err)
		}
		if client.baseURL != tc.wantURL || client.apiKey != tc.wantKey || client.http.Timeout != 60*time.Second {
			t.Error("vault fallback lost environment precedence, base path, or HTTP timeout")
		}
	}
}

func TestAuditMainHelper(t *testing.T) {
	encoded := os.Getenv("FLUXCTL_AUDIT_MAIN_ARGS")
	if encoded == "" {
		return
	}
	var args []string
	if err := json.Unmarshal([]byte(encoded), &args); err != nil {
		t.Fatal(err)
	}
	os.Args = append([]string{"fluxctl"}, args...)
	main()
	os.Exit(0)
}

func TestAuditMainExitAndJSONContracts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/entries/1":
			w.WriteHeader(401)
			fmt.Fprint(w, `{"error_message":"access unauthorized"}`)
		case "/v1/entries/2":
			w.WriteHeader(404)
			fmt.Fprint(w, `{"error_message":"resource not found"}`)
		case "/v1/entries/3":
			fmt.Fprint(w, "<html>login</html>")
		case "/v1/entries/4/fetch-content":
			fmt.Fprint(w, `{"content":"","reading_time":0}`)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		args  []string
		exit  int
		field string
		want  any
		help  bool
	}{
		{"unauthorized", []string{"entry", "get", "1"}, 1, "error", "miniflux rejected the API key", false},
		{"not found", []string{"entry", "get", "2"}, 1, "status", float64(404), false},
		{"malformed full", []string{"entry", "get", "3", "--full"}, 1, "error", "decoding miniflux response: invalid JSON", false},
		{"empty content", []string{"entry", "fetch", "4"}, 0, "content", "", false},
		{"bad version arguments", []string{"version", "unexpected"}, 1, "error", `version: unexpected argument "unexpected"`, false},
		{"version", []string{"version"}, 0, "version", version, false},
		{"leaf help", []string{"entry", "get", "--help"}, 0, "", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := json.Marshal(tc.args)
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(executable, "-test.run=^TestAuditMainHelper$")
			cmd.Env = append(os.Environ(), "MINIFLUX_URL="+server.URL, "MINIFLUX_API_KEY=audit-dummy-token", "FLUXCTL_AUDIT_MAIN_ARGS="+string(encoded))
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err = cmd.Run()
			exit := 0
			if err != nil {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) {
					t.Fatal(err)
				}
				exit = exitErr.ExitCode()
			}
			if exit != tc.exit {
				t.Fatalf("exit=%d want %d, stderr=%s", exit, tc.exit, &stderr)
			}
			if tc.help {
				if stdout.Len() != 0 || !strings.Contains(stderr.String(), "Usage:") {
					t.Fatalf("help output stdout=%s stderr=%s", &stdout, &stderr)
				}
				return
			}
			payload, other := stdout.Bytes(), stderr.Bytes()
			if tc.exit != 0 {
				payload, other = other, payload
			}
			var result map[string]any
			if len(other) != 0 || json.Unmarshal(payload, &result) != nil || result[tc.field] != tc.want {
				t.Fatalf("JSON contract: stdout=%s stderr=%s", &stdout, &stderr)
			}
			if tc.name == "unauthorized" && (result["fix"] == nil || result["details"] == nil) {
				t.Fatal("401 remedy missing")
			}
		})
	}
}

func TestAuditQuotedBlockAttributes(t *testing.T) {
	for _, input := range []string{`<p title="a>b">Body</p>`, `<div title='a>b'>Body</div>`, `<br title="a>b">Body`, `<hr title='a>b'>Body`} {
		if got := htmlToText(input); got != "Body" {
			t.Errorf("htmlToText(%q)=%q, want Body", input, got)
		}
	}
}
