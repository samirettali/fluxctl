package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type userCommandCase struct {
	name     string
	args     []string
	exchange runnerExchange
	want     string
}

func userCommandCases() []userCommandCase {
	return []userCommandCase{
		{"discover", []string{"feed", "discover", "--url", "https://example.com", "--disable-http2=false"}, runnerExchange{method: "POST", path: "/discover", body: `{"url":"https://example.com","disable_http2":false}`, response: `[{"title":"News","url":"https://example.com/rss","type":"rss"}]`}, `[{"title":"News","url":"https://example.com/rss","type":"rss"}]`},
		{"feed create", []string{"feed", "create", "--feed-url", "https://example.com/rss", "--category", "3", "--crawler=false"}, runnerExchange{method: "POST", path: "/feeds", body: `{"feed_url":"https://example.com/rss","category_id":3,"crawler":false}`, response: `{"feed_id":7}`}, `{"feed_id":7}`},
		{"feed update", []string{"feed", "update", "--title", "Renamed", "7", "--disabled=false"}, runnerExchange{method: "PUT", path: "/feeds/7", body: `{"title":"Renamed","disabled":false}`, response: runnerRawFeed}, runnerTrimmedFeed},
		{"feed delete", []string{"feed", "delete", "7"}, runnerExchange{method: "DELETE", path: "/feeds/7", status: 204}, `{"action":"feed delete","accepted":true,"id":7}`},
		{"feed refresh", []string{"feed", "refresh", "7"}, runnerExchange{method: "PUT", path: "/feeds/7/refresh", status: 204}, `{"action":"feed refresh","accepted":true,"id":7}`},
		{"feed refresh all", []string{"feed", "refresh", "--all"}, runnerExchange{method: "PUT", path: "/feeds/refresh", status: 204}, `{"action":"feed refresh","accepted":true,"all":true}`},
		{"feed mark all read", []string{"feed", "mark-all-read", "7"}, runnerExchange{method: "PUT", path: "/feeds/7/mark-all-as-read", status: 204}, `{"action":"feed mark-all-read","accepted":true,"id":7}`},
		{"feed icon", []string{"feed", "icon", "7"}, runnerExchange{method: "GET", path: "/feeds/7/icon", response: `{"id":9,"mime_type":"image/png","data":"image/png;base64,YQ=="}`}, `{"id":9,"mime_type":"image/png","data":"image/png;base64,YQ=="}`},
		{"category create", []string{"category", "create", "--title", "Tech", "--hide-globally=false"}, runnerExchange{method: "POST", path: "/categories", body: `{"title":"Tech","hide_globally":false}`, response: `{"id":3,"title":"Tech","hide_globally":false,"user_id":99}`}, `{"id":3,"title":"Tech","hide_globally":false}`},
		{"category update", []string{"category", "update", "3", "--hide-globally=false"}, runnerExchange{method: "PUT", path: "/categories/3", body: `{"hide_globally":false}`, response: `{"id":3,"title":"Tech","hide_globally":false}`}, `{"id":3,"title":"Tech","hide_globally":false}`},
		{"category delete", []string{"category", "delete", "3"}, runnerExchange{method: "DELETE", path: "/categories/3", status: 204}, `{"action":"category delete","accepted":true,"id":3}`},
		{"category refresh", []string{"category", "refresh", "3"}, runnerExchange{method: "PUT", path: "/categories/3/refresh", status: 204}, `{"action":"category refresh","accepted":true,"id":3}`},
		{"category mark all read", []string{"category", "mark-all-read", "3"}, runnerExchange{method: "PUT", path: "/categories/3/mark-all-as-read", status: 204}, `{"action":"category mark-all-read","accepted":true,"id":3}`},
		{"entry update", []string{"entry", "update", "42", "--title", "Changed"}, runnerExchange{method: "PUT", path: "/entries/42", body: `{"title":"Changed"}`, response: runnerRawEntry}, `{` + runnerTrimmedEntryFields + `,"content":"Hello & world."}`},
		{"entry import", []string{"entry", "import", "7", "--url", "https://example.com/article", "--starred=false", "--status", "unread"}, runnerExchange{method: "POST", path: "/feeds/7/entries/import", body: `{"url":"https://example.com/article","starred":false,"status":"unread"}`, response: `{"id":42}`}, `{"id":42}`},
		{"flush history", []string{"entry", "flush-history"}, runnerExchange{method: "DELETE", path: "/flush-history", status: 202}, `{"action":"entry flush-history","accepted":true}`},
		{"fetch update", []string{"entry", "fetch-update", "42", "--html"}, runnerExchange{method: "GET", path: "/entries/42/fetch-content", query: "update_content=true", response: `{"content":"<p>Full text</p>","reading_time":3}`}, `{"id":42,"content":"<p>Full text</p>","reading_time":3}`},
		{"enclosure get", []string{"enclosure", "get", "8"}, runnerExchange{method: "GET", path: "/enclosures/8", response: `{"id":8,"entry_id":42,"user_id":99,"url":"https://example.com/a.mp3","mime_type":"audio/mpeg","size":123,"media_progression":4}`}, `{"id":8,"entry_id":42,"url":"https://example.com/a.mp3","mime_type":"audio/mpeg","size":123,"media_progression":4}`},
		{"enclosure update", []string{"enclosure", "update", "--media-progression", "0", "8"}, runnerExchange{method: "PUT", path: "/enclosures/8", body: `{"media_progression":0}`, status: 204}, `{"action":"enclosure update","accepted":true,"id":8}`},
		{"icon get", []string{"icon", "get", "9", "--full"}, runnerExchange{method: "GET", path: "/icons/9", response: `{"id":9,"mime_type":"image/png","data":"image/png;base64,YQ==","extra":true}`}, `{"id":9,"mime_type":"image/png","data":"image/png;base64,YQ==","extra":true}`},
		{"integration status", []string{"integration", "status"}, runnerExchange{method: "GET", path: "/integrations/status", response: `{"has_integrations":false}`}, `{"has_integrations":false}`},
		{"server version", []string{"server", "version"}, runnerExchange{method: "GET", path: "/version", response: `{"version":"2.3.3","commit":"dummy"}`}, `{"version":"2.3.3","commit":"dummy"}`},
		{"entry ids", []string{"entry", "ids", "--starred=false", "--status", "read", "--limit", "20", "--offset", "2"}, runnerExchange{method: "GET", path: "/entries/ids", query: "limit=20&offset=2&starred=false&status=read", response: `{"total":3,"entry_ids":[42]}`}, `{"total":3,"entry_ids":[42]}`},
	}
}

func TestUserCommandRunners(t *testing.T) {
	for _, tc := range userCommandCases() {
		t.Run(tc.name, func(t *testing.T) { runnerServer(t, tc.exchange); checkRunnerSuccess(t, tc.want, tc.args...) })
		for _, status := range []int{401, 404, 500} {
			t.Run(fmt.Sprintf("%s/%d", tc.name, status), func(t *testing.T) {
				exchange := tc.exchange
				exchange.status = status
				exchange.response = `{"error_message":"dummy failure"}`
				runnerServer(t, exchange)
				output, err := captureRunner(t, tc.args...)
				if err == nil || output != "" {
					t.Fatalf("output=%s err=%v", output, err)
				}
				var api *APIError
				if !errors.As(err, &api) || api.Status != status {
					t.Fatalf("lost status: %v", err)
				}
				var auth *authError
				if status == 401 && !errors.As(err, &auth) {
					t.Fatalf("lost auth remedy: %v", err)
				}
			})
		}
		t.Run(tc.name+"/invalid JSON", func(t *testing.T) {
			exchange := tc.exchange
			exchange.status = 200
			exchange.response = `<html>broken</html>`
			runnerServer(t, exchange)
			if output, err := captureRunner(t, tc.args...); err == nil || output != "" {
				t.Fatalf("output=%s err=%v", output, err)
			}
		})
	}
}

func TestCurrentAccountMarkAllRead(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		runnerServer(t, runnerExchange{method: "GET", path: "/me", response: `{"id":99}`}, runnerExchange{method: "PUT", path: "/users/99/mark-all-as-read", status: 204})
		checkRunnerSuccess(t, `{"action":"entry mark-all-read","id":99,"accepted":true}`, "entry", "mark-all-read")
	})
	for _, body := range []string{`{}`, `{"id":null}`, `{"id":0}`, `{"id":"99"}`} {
		t.Run(body, func(t *testing.T) {
			runnerServer(t, runnerExchange{method: "GET", path: "/me", response: body})
			if _, err := captureRunner(t, "entry", "mark-all-read"); err == nil {
				t.Fatal("accepted invalid identity")
			}
		})
	}
	for _, status := range []int{401, 403, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			runnerServer(t, runnerExchange{method: "GET", path: "/me", status: status})
			if _, err := captureRunner(t, "entry", "mark-all-read"); err == nil {
				t.Fatal("accepted failed identity lookup")
			}
		})
	}
}

func buildUserCLI(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fluxctl")
	cmd := exec.Command("go", "build", "-o", path, ".")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	return path
}

func invokeUserCLI(t *testing.T, binary string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	exit := 0
	if err != nil {
		var e *exec.ExitError
		if !errors.As(err, &e) {
			t.Fatal(err)
		}
		exit = e.ExitCode()
	}
	return stdout.String(), stderr.String(), exit
}

func TestUserCommandsRealCLI(t *testing.T) {
	binary := buildUserCLI(t)
	for _, tc := range userCommandCases() {
		for _, mode := range []string{"success", "auth", "error", "malformed"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				exchange := tc.exchange
				switch mode {
				case "auth":
					exchange.status = 401
					exchange.response = `{"error_message":"dummy"}`
				case "error":
					exchange.status = 400
					exchange.response = `{"error_message":"dummy"}`
				case "malformed":
					exchange.status = 200
					exchange.response = "not JSON"
				}
				runnerServer(t, exchange)
				stdout, stderr, exit := invokeUserCLI(t, binary, tc.args...)
				if mode == "success" {
					if exit != 0 || stderr != "" {
						t.Fatalf("exit=%d stderr=%s", exit, stderr)
					}
					checkRunnerJSON(t, stdout, tc.want)
					return
				}
				var result map[string]any
				if exit != 1 || stdout != "" || json.Unmarshal([]byte(stderr), &result) != nil || result["error"] == nil {
					t.Fatalf("exit=%d stdout=%s stderr=%s", exit, stdout, stderr)
				}
				if mode == "auth" && result["fix"] == nil {
					t.Fatalf("missing remedy: %s", stderr)
				}
			})
		}
	}
	t.Run("current user", func(t *testing.T) {
		runnerServer(t, runnerExchange{method: "GET", path: "/me", response: `{"id":99}`}, runnerExchange{method: "PUT", path: "/users/99/mark-all-as-read", status: 204})
		out, stderr, exit := invokeUserCLI(t, binary, "entry", "mark-all-read")
		if exit != 0 || stderr != "" {
			t.Fatal(stderr)
		}
		checkRunnerJSON(t, out, `{"action":"entry mark-all-read","accepted":true,"id":99}`)
	})
	for _, group := range []string{"feed", "category", "entry", "opml", "icon", "enclosure", "server", "integration"} {
		t.Run("help "+group, func(t *testing.T) {
			out, stderr, exit := invokeUserCLI(t, binary, group, "--help")
			if exit != 0 || out != "" || !strings.Contains(stderr, "Usage:") {
				t.Fatalf("%d %s %s", exit, out, stderr)
			}
		})
	}
}

func inputFile(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input")
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUserInputFields(t *testing.T) {
	t.Run("override and omission", func(t *testing.T) {
		input := inputFile(t, `{"feed_url":"https://example.com/rss","category_id":3,"crawler":true,"disabled":false,"cookie":"dummy-cookie","password":"dummy-password","username":"subscriber","proxy_url":"http://user:dummy-proxy@proxy.example"}`)
		runnerServer(t, runnerExchange{method: "POST", path: "/feeds", body: `{"feed_url":"https://example.com/rss","category_id":3,"crawler":false,"disabled":false,"cookie":"dummy-cookie","password":"dummy-password","username":"subscriber","proxy_url":"http://user:dummy-proxy@proxy.example"}`, response: `{"feed_id":7}`})
		checkRunnerSuccess(t, `{"feed_id":7}`, "feed", "create", "--input", input, "--crawler=false")
	})
	t.Run("all update fields", func(t *testing.T) {
		body := map[string]any{}
		for field, kind := range feedFields("update") {
			switch kind {
			case "bool":
				body[field] = false
			case "int":
				body[field] = int64(3)
			default:
				body[field] = "dummy"
			}
		}
		data, _ := json.Marshal(body)
		input := inputFile(t, string(data))
		runnerServer(t, runnerExchange{method: "PUT", path: "/feeds/7", body: string(data), response: runnerRawFeed})
		if _, err := captureRunner(t, "feed", "update", "7", "--input", input); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("article import", func(t *testing.T) {
		input := inputFile(t, `{"url":"https://example.com/a","title":"Imported","published_at":10,"tags":["go","rss"],"external_id":"external","comments_url":"https://example.com/comments","author":"Ada"}`)
		content := inputFile(t, "<p>Article</p>")
		runnerServer(t, runnerExchange{method: "POST", path: "/feeds/7/entries/import", body: `{"url":"https://example.com/a","title":"Imported","published_at":10,"tags":["go","rss"],"external_id":"external","comments_url":"https://example.com/comments","author":"Ada","content":"<p>Article</p>"}`, response: `{"id":42}`})
		checkRunnerSuccess(t, `{"id":42}`, "entry", "import", "--input", input, "7", "--content-file", content)
	})
	for _, data := range []string{`not-json-dummy-secret`, `null`, `[]`, `{"unknown":"dummy-secret"}`, `{"password":null}`, `{"crawler":"dummy-secret"}`, `{"category_id":1.5}`} {
		t.Run(data, func(t *testing.T) {
			runnerServer(t)
			_, err := captureRunner(t, "feed", "create", "--input", inputFile(t, data))
			if err == nil || strings.Contains(err.Error(), "dummy-secret") {
				t.Fatalf("unsafe error %v", err)
			}
		})
	}
}

func TestUserCommandsLocalValidation(t *testing.T) {
	cases := [][]string{
		{"feed", "create"}, {"feed", "discover"}, {"feed", "update", "7"}, {"feed", "delete", "0"}, {"feed", "delete", "7", "8"}, {"feed", "refresh"}, {"feed", "refresh", "7", "--all"},
		{"feed", "create", "--feed-url", "url", "--category", "0"}, {"feed", "create", "--password", "dummy"}, {"feed", "update", "7", "--title", ""},
		{"category", "create"}, {"category", "update", "3"}, {"category", "delete"}, {"category", "refresh", "3", "stray"},
		{"entry", "update", "42", "--content", ""}, {"entry", "import", "7"}, {"entry", "import", "7", "--url", "url", "--published-at", "0"}, {"entry", "import", "7", "--url", "url", "--status", "removed"},
		{"entry", "mark-all-read", "99"}, {"entry", "flush-history", "42"}, {"enclosure", "update", "8"}, {"enclosure", "update", "8", "--media-progression", "-1"},
		{"icon", "get", "-1"}, {"integration", "status", "stray"}, {"server", "version", "stray"}, {"entry", "ids", "--limit", "0"}, {"entry", "ids", "--limit", "10001"}, {"entry", "ids", "--offset", "-1"}, {"entry", "ids", "--status", "removed"},
		{"opml", "import"}, {"opml", "export", "--output", "-"}, {"opml", "unknown"}, {"icon", "unknown"}, {"server"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			runnerServer(t)
			if out, err := captureRunner(t, args...); err == nil || out != "" {
				t.Fatalf("out=%s err=%v", out, err)
			}
		})
	}
}

func TestUserMalformedResponses(t *testing.T) {
	for _, tc := range userCommandCases() {
		if tc.exchange.response == "" {
			continue
		}
		args := append([]string(nil), tc.args...)
		for i, a := range args {
			if a == "--full" {
				args = append(args[:i], args[i+1:]...)
				break
			}
		}
		t.Run(tc.name, func(t *testing.T) {
			exchange := tc.exchange
			exchange.response = `{}`
			runnerServer(t, exchange)
			if _, err := captureRunner(t, args...); err == nil {
				t.Fatal("accepted invalid response shape")
			}
		})
	}
}

func TestEntryExtraReadFilters(t *testing.T) {
	runnerServer(t, runnerExchange{method: "GET", path: "/entries", query: "after_entry_id=2&before_entry_id=20&direction=desc&globally_visible=false&limit=50&order=published_at&starred=false&status=unread&tags=go&tags=rss", response: `{"total":0,"entries":[]}`})
	checkRunnerSuccess(t, `{"total":0,"entries":[]}`, "entry", "list", "--tag", "go", "--tag", "rss", "--before-id", "20", "--after-id", "2", "--globally-visible=false", "--starred=false")
}

func TestFetchUpdateRedirectDoesNotDropMutation(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; http.Redirect(w, r, "/lost", 302) }))
	defer server.Close()
	t.Setenv("MINIFLUX_URL", server.URL)
	t.Setenv("MINIFLUX_API_KEY", "dummy-key")
	if _, err := captureRunner(t, "entry", "fetch-update", "42"); err == nil {
		t.Fatal("accepted redirect")
	}
	if calls != 1 {
		t.Fatalf("retried mutation %d times", calls)
	}
}
