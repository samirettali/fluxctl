package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
