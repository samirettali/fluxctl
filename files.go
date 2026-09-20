package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func readInputFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("input must be an existing regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot open input file")
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("input must be a regular file")
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, errors.New("cannot read input file")
	}
	return data, nil
}

func validateOPML(data []byte) error {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	root, depth := false, 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errors.New("invalid OPML XML")
		}
		switch t := token.(type) {
		case xml.StartElement:
			if depth == 0 {
				if root || t.Name.Local != "opml" {
					return errors.New("expected one OPML document")
				}
				root = true
			}
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth == 0 && len(bytes.TrimSpace(t)) != 0 {
				return errors.New("text outside OPML document")
			}
		case xml.Directive:
			return errors.New("OPML XML directives are not supported")
		}
	}
	if !root || depth != 0 {
		return errors.New("empty or incomplete OPML document")
	}
	return nil
}

func runOPML(action string, args []string) error {
	if action != "import" && action != "export" {
		return fmt.Errorf("opml: unknown subcommand %q", action)
	}
	fs := newFlagSet("opml " + action)
	name := "input"
	if action == "export" {
		name = "output"
	}
	path := fs.String(name, "", "explicit local file path (exports never overwrite)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if err := noArgs(fs); err != nil {
		return err
	}
	if *path == "" || *path == "-" {
		return fmt.Errorf("opml %s requires --%s with a local file path", action, name)
	}
	if action == "import" {
		data, err := readInputFile(*path)
		if err != nil {
			return err
		}
		if err := validateOPML(data); err != nil {
			return err
		}
		client, err := newMinifluxClient()
		if err != nil {
			return err
		}
		response, err := client.requestBytes("POST", "/import", nil, bytes.NewReader(data), "application/xml", "application/json")
		if err != nil {
			return err
		}
		var result struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(response, &result) != nil || result.Message == "" {
			return errors.New("decoding miniflux OPML import response")
		}
		return writeJSON(map[string]any{"action": "opml import", "accepted": true})
	}
	f, cleanup, err := stageExport(*path)
	if err != nil {
		return err
	}
	defer cleanup()
	client, err := newMinifluxClient()
	if err != nil {
		return err
	}
	data, err := client.requestBytes("GET", "/export", nil, nil, "", "application/xml")
	if err != nil {
		return err
	}
	if err := validateOPML(data); err != nil {
		return err
	}
	if err := publishExport(f, *path, data); err != nil {
		return err
	}
	return writeJSON(map[string]any{"output": *path, "bytes": len(data)})
}

// The public destination is never reserved or unlinked. Only a private staging
// directory and its file are cleaned up; no pathname identity check can make a
// later unlink of the public destination safe against concurrent replacement.
func stageExport(output string) (*os.File, func(), error) {
	if _, err := os.Lstat(output); err == nil {
		return nil, nil, errors.New("output already exists: use a new path")
	} else if !os.IsNotExist(err) {
		return nil, nil, errors.New("cannot inspect output path")
	}
	directory, err := os.MkdirTemp(filepath.Dir(output), ".fluxctl-export-*")
	if err != nil {
		return nil, nil, errors.New("cannot create private export staging directory")
	}
	staged := filepath.Join(directory, "export.opml")
	file, err := os.OpenFile(staged, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		_ = os.Remove(directory)
		return nil, nil, errors.New("cannot create private export staging file")
	}
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(staged)
		_ = os.Remove(directory)
	}
	return file, cleanup, nil
}

func publishExport(file *os.File, output string, data []byte) error {
	if _, err := file.Write(data); err != nil {
		return errors.New("cannot write export staging file")
	}
	if err := file.Close(); err != nil {
		return errors.New("cannot close export staging file")
	}
	// Staging in the destination directory keeps both names on one filesystem.
	// Link publishes the complete, closed file atomically without overwriting any
	// file or symlink created since preflight. Unsupported filesystems fail closed.
	if err := os.Link(file.Name(), output); err != nil {
		return errors.New("cannot publish output without overwriting: use an unused path on a filesystem supporting hard links")
	}
	return nil
}
