package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func assertNoExportStaging(t *testing.T, directory string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(directory, ".fluxctl-export-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("staging retained: %v %v", matches, err)
	}
}

func TestExportPrivateStagingAndPublication(t *testing.T) {
	output := filepath.Join(t.TempDir(), "out.opml")
	file, cleanup, err := stageExport(output)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	directory := filepath.Dir(file.Name())
	info, err := os.Stat(directory)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("staging directory permissions: %v %v", info, err)
	}
	info, err = file.Stat()
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("staging file permissions: %v %v", info, err)
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		t.Fatalf("public destination created before publication: %v", err)
	}
	if err := publishExport(file, output, []byte(testOPML)); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("extra")); err == nil {
		t.Fatal("published file descriptor still writable")
	}
	cleanup()
	data, err := os.ReadFile(output)
	if err != nil || string(data) != testOPML {
		t.Fatalf("published data changed during staging cleanup: %q %v", data, err)
	}
	info, err = os.Stat(output)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("output permissions: %v %v", info, err)
	}
	assertNoExportStaging(t, filepath.Dir(output))
}

// Schedule a replacement at the final cleanup boundary, not during HTTP. The
// old identity-check/unlink cleanup would delete it after its successful check;
// staging cleanup cannot unlink the public pathname at any point.
func TestExportCleanupNeverTouchesPublicDestination(t *testing.T) {
	for _, stage := range []string{"before publish", "write failure", "after publish"} {
		t.Run(stage, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "out.opml")
			file, cleanup, err := stageExport(output)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			if stage == "after publish" {
				if err := publishExport(file, output, []byte(testOPML)); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(output, output+".moved"); err != nil {
					t.Fatal(err)
				}
			}
			if stage == "write failure" {
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
				if err := publishExport(file, output, []byte(testOPML)); err == nil {
					t.Fatal("accepted write to closed staging file")
				}
			}
			// All export operations/checks have finished; immediately before cleanup a
			// different writer owns this pathname. No public cleanup syscall is allowed.
			if err := os.WriteFile(output, []byte("keep me"), 0600); err != nil {
				t.Fatal(err)
			}
			cleanup()
			if data, err := os.ReadFile(output); err != nil || string(data) != "keep me" {
				t.Fatalf("cleanup changed unrelated destination: %q %v", data, err)
			}
			assertNoExportStaging(t, filepath.Dir(output))
		})
	}
}

func TestExportNoClobberAtPublication(t *testing.T) {
	for _, kind := range []string{"file", "symlink", "directory"} {
		t.Run(kind, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "out.opml")
			file, cleanup, err := stageExport(output)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			target := inputFile(t, "keep target")
			switch kind {
			case "file":
				err = os.WriteFile(output, []byte("keep me"), 0600)
			case "symlink":
				err = os.Symlink(target, output)
			case "directory":
				err = os.Mkdir(output, 0700)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := publishExport(file, output, []byte(testOPML)); err == nil || !strings.Contains(err.Error(), "hard links") {
				t.Fatalf("publication did not fail closed: %v", err)
			}
			cleanup()
			switch kind {
			case "file":
				if data, err := os.ReadFile(output); err != nil || string(data) != "keep me" {
					t.Fatalf("changed file: %q %v", data, err)
				}
			case "symlink":
				if got, err := os.Readlink(output); err != nil || got != target {
					t.Fatalf("changed symlink: %q %v", got, err)
				}
			case "directory":
				if info, err := os.Stat(output); err != nil || !info.IsDir() {
					t.Fatalf("changed directory: %v", err)
				}
			}
			if data, err := os.ReadFile(target); err != nil || string(data) != "keep target" {
				t.Fatalf("changed target: %q %v", data, err)
			}
			assertNoExportStaging(t, filepath.Dir(output))
		})
	}
}

func TestExportCleanupFailureDoesNotRollBackPublishedFile(t *testing.T) {
	output := filepath.Join(t.TempDir(), "out.opml")
	file, cleanup, err := stageExport(output)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := publishExport(file, output, []byte(testOPML)); err != nil {
		t.Fatal(err)
	}
	// Force staging-directory removal to fail, without touching the destination.
	blocker := filepath.Join(filepath.Dir(file.Name()), "blocker")
	if err := os.WriteFile(blocker, []byte("keep staging directory"), 0600); err != nil {
		t.Fatal(err)
	}
	cleanup()
	if _, err := os.Stat(filepath.Dir(file.Name())); err != nil {
		t.Fatalf("expected cleanup failure: %v", err)
	}
	if data, err := os.ReadFile(output); err != nil || string(data) != testOPML {
		t.Fatalf("cleanup failure rolled back publication: %q %v", data, err)
	}
}

func TestExportConcurrentDestinationCLI(t *testing.T) {
	binary := buildUserCLI(t)
	for _, response := range []string{"success", "auth", "server error", "invalid XML"} {
		t.Run(response, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "out.opml")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, err := os.Lstat(output); !os.IsNotExist(err) {
					t.Errorf("public destination reserved: %v", err)
				}
				if err := os.WriteFile(output, []byte("keep me"), 0600); err != nil {
					t.Errorf("concurrent writer: %v", err)
				}
				switch response {
				case "success":
					fmt.Fprint(w, testOPML)
				case "auth":
					w.WriteHeader(401)
				case "server error":
					w.WriteHeader(500)
				case "invalid XML":
					fmt.Fprint(w, "broken XML")
				}
			}))
			defer server.Close()
			t.Setenv("MINIFLUX_URL", server.URL)
			t.Setenv("MINIFLUX_API_KEY", "dummy-key")
			out, stderr, exit := invokeUserCLI(t, binary, "opml", "export", "--output", output)
			if exit != 1 || out != "" || stderr == "" {
				t.Fatalf("expected failure: %d %s %s", exit, out, stderr)
			}
			if data, err := os.ReadFile(output); err != nil || string(data) != "keep me" {
				t.Fatalf("changed concurrent destination: %q %v", data, err)
			}
			assertNoExportStaging(t, filepath.Dir(output))
		})
	}
}
