package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const secretFeed = `{"id":7,"title":"Subscriptions","site_url":"https://example.com","feed_url":"https://reader:dummy-url-secret@example.com/rss","category":{"id":3,"title":"Tech"},"username":"reader","password":"dummy-password-secret","cookie":"dummy-cookie-secret","proxy_url":"http://proxy-user:dummy-proxy-secret@proxy.example:8080?token=dummy-proxy-query","webhook_url":"https://hooks.example/dummy-webhook-secret","apprise_service_urls":"https://notify.example/dummy-notify-secret","parsing_error_message":"dummy-password-secret dummy-cookie-secret dummy-proxy-secret dummy-proxy-query","parsing_error_count":1}`

func assertNoDummySecrets(t *testing.T, output string) {
	t.Helper()
	for _, secret := range []string{"dummy-password-secret", "dummy-cookie-secret", "dummy-proxy-secret", "dummy-proxy-query", "dummy-url-secret", "dummy-webhook-secret", "dummy-notify-secret"} {
		if strings.Contains(output, secret) {
			t.Errorf("leaked %s in %s", secret, output)
		}
	}
}

func TestResponseCredentialsAlwaysRedacted(t *testing.T) {
	for _, full := range []bool{false, true} {
		for _, tc := range []struct {
			args     []string
			exchange runnerExchange
		}{
			{[]string{"feed", "get", "7"}, runnerExchange{method: "GET", path: "/feeds/7", response: secretFeed}},
			{[]string{"feed", "list"}, runnerExchange{method: "GET", path: "/feeds", response: `[` + secretFeed + `]`}},
			{[]string{"feed", "list", "--category", "3"}, runnerExchange{method: "GET", path: "/categories/3/feeds", response: `[` + secretFeed + `]`}},
			{[]string{"feed", "update", "7", "--disabled=false"}, runnerExchange{method: "PUT", path: "/feeds/7", body: `{"disabled":false}`, response: secretFeed}},
			{[]string{"entry", "get", "42"}, runnerExchange{method: "GET", path: "/entries/42", response: `{"id":42,"feed":` + secretFeed + `,"content":"dummy-password-secret"}`}},
			{[]string{"entry", "list"}, runnerExchange{method: "GET", path: "/entries", query: "direction=desc&limit=50&order=published_at&status=unread", response: `{"total":1,"entries":[{"id":42,"feed":` + secretFeed + `}]}`}},
			{[]string{"entry", "update", "42", "--title", "New"}, runnerExchange{method: "PUT", path: "/entries/42", body: `{"title":"New"}`, response: `{"id":42,"feed":` + secretFeed + `}`}},
		} {
			name := fmt.Sprintf("%s/full=%t", strings.Join(tc.args, " "), full)
			t.Run(name, func(t *testing.T) {
				runnerServer(t, tc.exchange)
				args := append([]string(nil), tc.args...)
				if full {
					args = append(args, "--full")
				}
				out, err := captureRunner(t, args...)
				if err != nil {
					t.Fatal(err)
				}
				assertNoDummySecrets(t, out)
				if !json.Valid([]byte(out)) {
					t.Fatal("invalid JSON")
				}
			})
		}
	}
	t.Run("preserve nonsecret fields", func(t *testing.T) {
		got := redactResponse(json.RawMessage(secretFeed))
		assertNoDummySecrets(t, string(got))
		var fields map[string]any
		if json.Unmarshal(got, &fields) != nil {
			t.Fatal("bad JSON")
		}
		if fields["username"] != "reader" || fields["title"] != "Subscriptions" || fields["proxy_url"] != "http://proxy.example:8080" || fields["feed_url"] != "https://example.com/rss" || fields["password"] != redacted || fields["cookie"] != redacted {
			t.Fatalf("unexpected redaction: %s", got)
		}
	})
	t.Run("large IDs", func(t *testing.T) {
		got := redactResponse(json.RawMessage(`{"id":9223372036854775807,"password":"dummy-password-secret"}`))
		if !strings.Contains(string(got), `9223372036854775807`) {
			t.Fatal("lost integer precision")
		}
	})
}

func TestSensitiveInputErrorsAndSuccess(t *testing.T) {
	binary := buildUserCLI(t)
	input := inputFile(t, `{"feed_url":"https://example.com/rss","password":"dummy-password-secret","cookie":"dummy-cookie-secret","proxy_url":"http://user:dummy-proxy-secret@proxy.example?token=dummy-proxy-query"}`)
	for _, mode := range []string{"success", "auth", "error", "invalid json", "malformed redirect", "credential redirect", "cross-origin redirect"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				switch mode {
				case "success":
					fmt.Fprint(w, `{"feed_id":7,"message":"dummy-password-secret dummy-cookie-secret dummy-proxy-secret dummy-proxy-query"}`)
				case "auth":
					w.WriteHeader(401)
					fmt.Fprint(w, `{"error_message":"dummy-password-secret dummy-cookie-secret dummy-proxy-secret dummy-proxy-query"}`)
				case "error":
					w.WriteHeader(500)
					fmt.Fprint(w, `{"error_message":"dummy-password-secret dummy-cookie-secret dummy-proxy-secret dummy-proxy-query"}`)
				case "invalid json":
					fmt.Fprint(w, `not-json dummy-password-secret`)
				case "malformed redirect":
					w.Header().Set("Location", "http://user:dummy-password-secret@host/%bad%")
					w.WriteHeader(307)
				case "credential redirect":
					w.Header().Set("Location", "http://user:dummy-password-secret@"+r.Host+"/v1/feeds")
					w.WriteHeader(307)
				case "cross-origin redirect":
					w.Header().Set("Location", "https://other.invalid/dummy-password-secret")
					w.WriteHeader(307)
				}
			}))
			defer server.Close()
			t.Setenv("MINIFLUX_URL", server.URL)
			t.Setenv("MINIFLUX_API_KEY", "dummy-key")
			out, stderr, exit := invokeUserCLI(t, binary, "feed", "create", "--input", input, "--full")
			assertNoDummySecrets(t, out+stderr)
			if mode == "success" {
				if exit != 0 || stderr != "" {
					t.Fatalf("%d %s", exit, stderr)
				}
			} else if exit != 1 || out != "" || !json.Valid([]byte(stderr)) {
				t.Fatalf("%d %s %s", exit, out, stderr)
			}
			if mode == "auth" && !strings.Contains(stderr, `"fix"`) {
				t.Fatal("missing auth remedy")
			}
			if calls != 1 {
				t.Fatalf("replayed mutation: %d", calls)
			}
		})
	}
}

// R1: an allowed redirect can echo credentials in its path and fail only after
// net/http has accepted it. Both JSON and OPML must strip url.Error's URL.
func TestAcceptedRedirectTransportFailureDoesNotLeakCLI(t *testing.T) {
	binary := buildUserCLI(t)
	for _, operation := range []string{"feed create", "opml import", "opml export"} {
		t.Run(operation, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if strings.HasPrefix(r.URL.Path, "/v1/") {
					w.Header().Set("Location", "/redirect/dummy-password-secret/dummy-opml-secret")
					w.WriteHeader(http.StatusTemporaryRedirect)
					return
				}
				connection, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Errorf("hijack: %v", err)
					return
				}
				_ = connection.Close()
			}))
			defer server.Close()
			t.Setenv("MINIFLUX_URL", server.URL)
			t.Setenv("MINIFLUX_API_KEY", "dummy-key")
			var args []string
			output := filepath.Join(t.TempDir(), "export.opml")
			switch operation {
			case "feed create":
				args = []string{"feed", "create", "--input", inputFile(t, `{"feed_url":"https://example.com/rss","password":"dummy-password-secret"}`)}
			case "opml import":
				args = []string{"opml", "import", "--input", inputFile(t, testOPML)}
			case "opml export":
				args = []string{"opml", "export", "--output", output}
			}
			stdout, stderr, exit := invokeUserCLI(t, binary, args...)
			assertNoDummySecrets(t, stdout+stderr)
			if strings.Contains(stderr, "dummy-opml-secret") || strings.Contains(stderr, server.URL) {
				t.Fatalf("redirect URL leaked: %s", stderr)
			}
			var result map[string]string
			if exit != 1 || stdout != "" || json.Unmarshal([]byte(stderr), &result) != nil || !strings.HasPrefix(result["error"], "calling miniflux: ") {
				t.Fatalf("lost safe transport failure: exit=%d stdout=%s stderr=%s", exit, stdout, stderr)
			}
			if calls.Load() < 2 || (operation != "opml export" && calls.Load() != 2) {
				t.Fatalf("unexpected request count: %d", calls.Load())
			}
			if operation == "opml export" {
				if _, err := os.Lstat(output); !os.IsNotExist(err) {
					t.Fatalf("failed export retained output: %v", err)
				}
			}
		})
	}
}

func TestAuthenticatedURLsAreFileOnly(t *testing.T) {
	for _, args := range [][]string{
		{"feed", "discover", "--url", "https://reader:dummy-password-secret@example.com"},
		{"feed", "create", "--feed-url", "https://reader:dummy-password-secret@example.com/rss"},
		{"feed", "update", "7", "--site-url", "http://reader:dummy-password-secret@[invalid"},
	} {
		t.Run(args[1], func(t *testing.T) {
			runnerServer(t)
			out, err := captureRunner(t, args...)
			if err == nil || out != "" {
				t.Fatalf("accepted authenticated URL: %s %v", out, err)
			}
			assertNoDummySecrets(t, err.Error())
		})
	}
	t.Run("file accepted", func(t *testing.T) {
		input := inputFile(t, `{"feed_url":"https://reader:dummy-password-secret@example.com/rss"}`)
		runnerServer(t, runnerExchange{method: "POST", path: "/feeds", body: `{"feed_url":"https://reader:dummy-password-secret@example.com/rss"}`, response: `{"feed_id":7}`})
		checkRunnerSuccess(t, `{"feed_id":7}`, "feed", "create", "--input", input)
	})
}

func TestMalformedSecretInputDoesNotEchoContents(t *testing.T) {
	binary := buildUserCLI(t)
	for _, data := range []string{`{"password":"dummy-password-secret"`, `{"password":123,"cookie":"dummy-cookie-secret"}`, `{"dummy-password-secret":true}`} {
		t.Run(data, func(t *testing.T) {
			runnerServer(t)
			out, stderr, exit := invokeUserCLI(t, binary, "feed", "create", "--input", inputFile(t, data))
			assertNoDummySecrets(t, out+stderr)
			if exit != 1 || out != "" || !json.Valid([]byte(stderr)) {
				t.Fatalf("%d %s %s", exit, out, stderr)
			}
		})
	}
}
