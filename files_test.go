package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testOPML = `<?xml version="1.0"?><opml version="2.0"><head><title>Subscriptions</title></head><body><outline text="News" type="rss" xmlUrl="https://user:dummy-opml-secret@example.com/rss"/></body></opml>`

func TestOPMLRealCLI(t *testing.T) {
	binary := buildUserCLI(t)
	for _, action := range []string{"import", "export"} {
		for _, mode := range []string{"success", "auth", "error", "malformed", "empty"} {
			t.Run(action+"/"+mode, func(t *testing.T) {
				calls := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					method, accept := "POST", "application/json"
					if action == "export" {
						method, accept = "GET", "application/xml"
					}
					if r.Method != method || r.URL.Path != "/v1/"+map[string]string{"import": "import", "export": "export"}[action] || r.URL.RawQuery != "" {
						t.Errorf("request %s %s", r.Method, r.URL)
					}
					if r.Header.Get("Accept") != accept || r.Header.Get("X-Auth-Token") != "dummy-key" {
						t.Error("headers incorrect")
					}
					body, _ := io.ReadAll(r.Body)
					if action == "import" {
						if string(body) != testOPML || r.Header.Get("Content-Type") != "application/xml" {
							t.Errorf("invalid OPML body/content type")
						}
					} else if len(body) != 0 {
						t.Error("GET has body")
					}
					switch mode {
					case "auth":
						w.WriteHeader(401)
						fmt.Fprint(w, `{"error_message":"dummy-opml-secret"}`)
					case "error":
						w.WriteHeader(500)
						fmt.Fprint(w, "dummy-opml-secret")
					case "malformed":
						fmt.Fprint(w, "broken dummy-opml-secret")
					case "empty":
						return
					default:
						if action == "export" {
							fmt.Fprint(w, testOPML)
						} else {
							w.WriteHeader(201)
							fmt.Fprint(w, `{"message":"Imported dummy-opml-secret"}`)
						}
					}
				}))
				defer server.Close()
				t.Setenv("MINIFLUX_URL", server.URL)
				t.Setenv("MINIFLUX_API_KEY", "dummy-key")
				flag, path := "--input", inputFile(t, testOPML)
				if action == "export" {
					flag, path = "--output", filepath.Join(t.TempDir(), "subscriptions.opml")
				}
				out, stderr, exit := invokeUserCLI(t, binary, "opml", action, flag, path)
				if calls != 1 {
					t.Errorf("calls=%d", calls)
				}
				if strings.Contains(out+stderr, "dummy-opml-secret") {
					t.Fatal("OPML content leaked")
				}
				if mode == "success" {
					if exit != 0 || stderr != "" || !json.Valid([]byte(out)) {
						t.Fatalf("%d %s %s", exit, out, stderr)
					}
					if action == "import" {
						checkRunnerJSON(t, out, `{"action":"opml import","accepted":true}`)
					} else {
						data, err := os.ReadFile(path)
						if err != nil || string(data) != testOPML {
							t.Fatalf("output mismatch: %v", err)
						}
						info, _ := os.Stat(path)
						if info.Mode().Perm() != 0600 {
							t.Errorf("permissions %v", info.Mode())
						}
						var receipt struct {
							Output string `json:"output"`
							Bytes  int    `json:"bytes"`
						}
						if json.Unmarshal([]byte(out), &receipt) != nil || receipt.Output != path || receipt.Bytes != len(testOPML) {
							t.Fatalf("receipt %s", out)
						}
					}
				} else {
					if exit != 1 || out != "" || !json.Valid([]byte(stderr)) {
						t.Fatalf("%d %s %s", exit, out, stderr)
					}
					if mode == "auth" && !strings.Contains(stderr, `"fix"`) {
						t.Fatal("missing auth fix")
					}
					if action == "export" {
						if _, err := os.Lstat(path); !os.IsNotExist(err) {
							t.Fatalf("failed export left file: %v", err)
						}
					}
				}
			})
		}
	}
}

func TestOPMLLocalFileSafety(t *testing.T) {
	t.Run("existing file", func(t *testing.T) {
		runnerServer(t)
		path := inputFile(t, "keep me")
		if _, err := captureRunner(t, "opml", "export", "--output", path); err == nil {
			t.Fatal("overwrote file")
		}
		data, _ := os.ReadFile(path)
		if string(data) != "keep me" {
			t.Fatal("changed file")
		}
	})
	t.Run("symlink", func(t *testing.T) {
		runnerServer(t)
		target := inputFile(t, "keep me")
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, err := captureRunner(t, "opml", "export", "--output", link); err == nil {
			t.Fatal("followed symlink")
		}
		data, _ := os.ReadFile(target)
		if string(data) != "keep me" {
			t.Fatal("changed target")
		}
	})
	t.Run("dangling symlink", func(t *testing.T) {
		runnerServer(t)
		target := filepath.Join(t.TempDir(), "absent")
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, err := captureRunner(t, "opml", "export", "--output", link); err == nil {
			t.Fatal("replaced symlink")
		}
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Fatal("created target")
		}
	})
	for _, action := range []string{"import", "export"} {
		t.Run(action+" directory", func(t *testing.T) {
			runnerServer(t)
			flag := "--input"
			if action == "export" {
				flag = "--output"
			}
			if _, err := captureRunner(t, "opml", action, flag, t.TempDir()); err == nil {
				t.Fatal("accepted directory")
			}
		})
	}
	for _, body := range []string{"", `<html>dummy-opml-secret</html>`, `<opml>`, `<opml/><opml/>`, `<opml/>trailing`, `<!DOCTYPE opml [<!ENTITY x SYSTEM "file:///etc/passwd">]><opml>&x;</opml>`} {
		t.Run(body, func(t *testing.T) {
			runnerServer(t)
			_, err := captureRunner(t, "opml", "import", "--input", inputFile(t, body))
			if err == nil || strings.Contains(err.Error(), "dummy-opml-secret") {
				t.Fatalf("invalid XML error %v", err)
			}
		})
	}
	t.Run("missing input", func(t *testing.T) {
		runnerServer(t)
		if _, err := captureRunner(t, "opml", "import", "--input", filepath.Join(t.TempDir(), "absent")); err == nil {
			t.Fatal("accepted missing input")
		}
	})
	t.Run("missing output parent", func(t *testing.T) {
		runnerServer(t)
		if _, err := captureRunner(t, "opml", "export", "--output", filepath.Join(t.TempDir(), "absent", "file")); err == nil {
			t.Fatal("created missing parent")
		}
	})
}

func TestOPMLRedirectProtection(t *testing.T) {
	for _, action := range []string{"import", "export"} {
		t.Run(action, func(t *testing.T) {
			targetCalls := 0
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls++; t.Error("cross-origin request") }))
			defer target.Close()
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", target.URL+"/dummy-opml-secret")
				w.WriteHeader(307)
			}))
			defer source.Close()
			t.Setenv("MINIFLUX_URL", source.URL)
			t.Setenv("MINIFLUX_API_KEY", "dummy-key")
			flag, path := "--input", inputFile(t, testOPML)
			if action == "export" {
				flag, path = "--output", filepath.Join(t.TempDir(), "out")
			}
			_, err := captureRunner(t, "opml", action, flag, path)
			if err == nil || strings.Contains(err.Error(), "dummy-opml-secret") || targetCalls != 0 {
				t.Fatalf("unsafe redirect %v calls=%d", err, targetCalls)
			}
		})
	}
}
