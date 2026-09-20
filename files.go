package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
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
	// Reserve exclusively before making a request. Existing files, directories and
	// symlinks are never replaced; a failed export removes only our new file.
	f, err := os.OpenFile(*path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("cannot create output file: use a new path")
	}
	created, err := f.Stat()
	if err != nil {
		f.Close()
		return errors.New("cannot identify output file")
	}
	complete := false
	defer func() {
		f.Close()
		if !complete {
			// Another process may have moved our reservation and replaced its
			// pathname. Never remove that replacement or follow a new symlink.
			if current, err := os.Lstat(*path); err == nil && os.SameFile(created, current) {
				_ = os.Remove(*path)
			}
		}
	}()
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
	if _, err := f.Write(data); err != nil {
		return errors.New("cannot write output file")
	}
	if err := f.Close(); err != nil {
		return errors.New("cannot close output file")
	}
	complete = true
	return writeJSON(map[string]any{"output": *path, "bytes": len(data)})
}
