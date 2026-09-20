package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOPMLRunners(t *testing.T) {
	for _, action := range []string{"import", "export"} {
		for _, mode := range []string{"success", "auth", "error", "malformed", "empty", "wrong shape"} {
			t.Run(action+"/"+mode, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch mode {
					case "auth":
						w.WriteHeader(401)
					case "error":
						w.WriteHeader(500)
					case "malformed":
						fmt.Fprint(w, "broken XML/JSON")
					case "empty":
						return
					case "wrong shape":
						fmt.Fprint(w, `{}`)
					default:
						if action == "import" {
							fmt.Fprint(w, `{"message":"Imported"}`)
						} else {
							fmt.Fprint(w, testOPML)
						}
					}
				}))
				defer server.Close()
				t.Setenv("MINIFLUX_URL", server.URL)
				t.Setenv("MINIFLUX_API_KEY", "dummy-key")
				flag, path := "--input", inputFile(t, testOPML)
				if action == "export" {
					flag, path = "--output", filepath.Join(t.TempDir(), "out")
				}
				out, err := captureRunner(t, "opml", action, flag, path)
				if mode == "success" {
					if err != nil || !json.Valid([]byte(out)) {
						t.Fatalf("%s %v", out, err)
					}
				} else {
					if err == nil || out != "" {
						t.Fatalf("%s %v", out, err)
					}
					if action == "export" {
						if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
							t.Fatal("failed export file retained")
						}
					}
				}
			})
		}
	}
}

func TestNewCommandsFullResponses(t *testing.T) {
	for _, tc := range userCommandCases() {
		if tc.exchange.response == "" || tc.name == "fetch update" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			runnerServer(t, tc.exchange)
			args := append([]string(nil), tc.args...)
			args = append(args, "--full")
			checkRunnerSuccess(t, tc.exchange.response, args...)
		})
	}
}

func TestNewCommandHelp(t *testing.T) {
	for _, tc := range userCommandCases() {
		t.Run(tc.name, func(t *testing.T) {
			runnerServer(t)
			args := append(append([]string(nil), tc.args[:2]...), "--help")
			if _, err := captureRunner(t, args...); err != errHelp {
				t.Fatalf("help %v", err)
			}
		})
	}
	for _, args := range [][]string{{"opml", "import", "--help"}, {"opml", "export", "--help"}, {"opml", "export", "stray"}, {"entry", "ids", "stray"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			runnerServer(t)
			if _, err := captureRunner(t, args...); err == nil {
				t.Fatal("accepted help/stray")
			}
		})
	}
}

func TestContentAndInputFileFailures(t *testing.T) {
	for _, args := range [][]string{
		{"feed", "create", "--input", filepath.Join(t.TempDir(), "missing")},
		{"entry", "update", "42", "--content-file", filepath.Join(t.TempDir(), "missing")},
		{"entry", "update", "42", "--content", "existing", "--content-file", inputFile(t, "other")},
		{"entry", "list", "--before-id", "-1"},
		{"entry", "list", "--after-id", "-1"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			runnerServer(t)
			if _, err := captureRunner(t, args...); err == nil {
				t.Fatal("accepted invalid input")
			}
		})
	}
}

func TestNewCommandsInvalidConfiguration(t *testing.T) {
	t.Setenv("MINIFLUX_URL", "invalid-url")
	t.Setenv("MINIFLUX_API_KEY", "dummy-key")
	for _, args := range [][]string{
		{"feed", "delete", "7"}, {"entry", "ids"}, {"opml", "import", "--input", inputFile(t, testOPML)},
		{"opml", "export", "--output", filepath.Join(t.TempDir(), "out")},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if _, err := captureRunner(t, args...); err == nil {
				t.Fatal("accepted invalid config")
			}
			if args[0] == "opml" && args[1] == "export" {
				if _, err := os.Stat(args[3]); !os.IsNotExist(err) {
					t.Fatal("failed config left file")
				}
			}
		})
	}
}

func TestCurrentUserMutationFailureCLI(t *testing.T) {
	binary := buildUserCLI(t)
	for _, stage := range []string{"identity", "mutation"} {
		for _, status := range []int{401, 404, 500} {
			t.Run(fmt.Sprintf("%s/%d", stage, status), func(t *testing.T) {
				exchanges := []runnerExchange{{method: "GET", path: "/me", status: status, response: `{"error_message":"dummy"}`}}
				if stage == "mutation" {
					exchanges = []runnerExchange{{method: "GET", path: "/me", response: `{"id":99}`}, {method: "PUT", path: "/users/99/mark-all-as-read", status: status, response: `{"error_message":"dummy"}`}}
				}
				runnerServer(t, exchanges...)
				out, stderr, exit := invokeUserCLI(t, binary, "entry", "mark-all-read")
				if exit != 1 || out != "" || !json.Valid([]byte(stderr)) {
					t.Fatalf("%d %s %s", exit, out, stderr)
				}
				if status == 401 && !strings.Contains(stderr, `"fix"`) {
					t.Fatal("auth remedy absent")
				}
			})
		}
	}
	t.Run("malformed identity", func(t *testing.T) {
		runnerServer(t, runnerExchange{method: "GET", path: "/me", response: `{"id":null}`})
		out, stderr, exit := invokeUserCLI(t, binary, "entry", "mark-all-read")
		if exit != 1 || out != "" || !json.Valid([]byte(stderr)) {
			t.Fatalf("%d %s %s", exit, out, stderr)
		}
	})
}

func TestIDResponseValidation(t *testing.T) {
	for _, body := range []string{`null`, `{"total":1,"entry_ids":null}`, `{"total":-1,"entry_ids":[]}`, `{"total":1,"entry_ids":[0]}`, `{"total":1,"entry_ids":["1"]}`} {
		t.Run(body, func(t *testing.T) {
			runnerServer(t, runnerExchange{method: "GET", path: "/entries/ids", query: "limit=1000&offset=0", response: body})
			if _, err := captureRunner(t, "entry", "ids"); err == nil {
				t.Fatal("accepted malformed IDs")
			}
		})
	}
}

func TestAdditionalResponseValidation(t *testing.T) {
	for _, tc := range []struct{ kind, data string }{
		{"id", ""}, {"feed", `{}`}, {"entry", `{}`}, {"category", `{"id":3,"title":42}`}, {"discovery", `[{"title":"no URL"}]`},
		{"icon", `{"id":3}`}, {"enclosure", `{"id":3,"entry_id":0}`}, {"unknown", `{}`},
	} {
		t.Run(tc.kind+tc.data, func(t *testing.T) {
			if err := writeUserResponse(tc.kind, json.RawMessage(tc.data), false); err == nil {
				t.Fatal("accepted invalid response")
			}
		})
	}
}
