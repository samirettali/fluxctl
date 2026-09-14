package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

const rbwEntry = "miniflux-api-key"

// APIError is a non-2xx answer from Miniflux.
type APIError struct {
	Status  int
	Message string
	Details string
}

func (err *APIError) Error() string {
	if err.Details != "" {
		return fmt.Sprintf("%s (status %d): %s", err.Message, err.Status, err.Details)
	}
	return fmt.Sprintf("%s (status %d)", err.Message, err.Status)
}

// authError carries its own remedy so no caller has to probe configuration first.
type authError struct {
	Message string
	Fix     string
	Cause   error
}

func (err *authError) Error() string { return err.Message + ": " + err.Fix }
func (err *authError) Unwrap() error { return err.Cause }

func notConfigured(message string, cause error) error {
	return &authError{
		Message: message,
		Fix:     "export MINIFLUX_URL and MINIFLUX_API_KEY, or store the key as the password and the URL as the URI of the rbw entry " + rbwEntry,
		Cause:   cause,
	}
}

type minifluxClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// newMinifluxClient reads MINIFLUX_URL and MINIFLUX_API_KEY, falling back to the rbw vault
// when either is missing.
func newMinifluxClient() (*minifluxClient, error) {
	baseURL, apiKey := os.Getenv("MINIFLUX_URL"), os.Getenv("MINIFLUX_API_KEY")
	if baseURL == "" || apiKey == "" {
		vaultURL, vaultKey, err := readVault()
		if err != nil {
			return nil, notConfigured("miniflux is not configured", err)
		}
		if baseURL == "" {
			baseURL = vaultURL
		}
		if apiKey == "" {
			apiKey = vaultKey
		}
	}
	if baseURL == "" {
		return nil, notConfigured("miniflux URL is missing", nil)
	}
	if apiKey == "" {
		return nil, notConfigured("miniflux API key is missing", nil)
	}
	baseURL = strings.TrimRight(baseURL, "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, notConfigured(fmt.Sprintf("miniflux URL %q is invalid: expected http(s)://host", baseURL), err)
	}
	return &minifluxClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 60 * time.Second},
	}, nil
}

// readVault returns the base URL and API key stored in the rbw entry. It never prompts:
// a locked vault is an error, since pinentry has no terminal to ask on from an agent.
func readVault() (string, string, error) {
	if _, err := exec.LookPath("rbw"); err != nil {
		return "", "", errors.New("rbw is not installed")
	}
	if err := exec.Command("rbw", "unlocked").Run(); err != nil {
		return "", "", errors.New("rbw is locked: run 'rbw unlock'")
	}
	raw, err := exec.Command("rbw", "get", "--raw", rbwEntry).Output()
	if err != nil {
		return "", "", fmt.Errorf("rbw entry %q not found", rbwEntry)
	}
	var entry struct {
		Data struct {
			Password string `json:"password"`
			URIs     []struct {
				URI string `json:"uri"`
			} `json:"uris"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &entry); err != nil {
		return "", "", fmt.Errorf("decoding rbw entry %q: %w", rbwEntry, err)
	}
	baseURL := ""
	if len(entry.Data.URIs) > 0 {
		baseURL = entry.Data.URIs[0].URI
	}
	return baseURL, entry.Data.Password, nil
}

// request performs one API call. A 2xx with an empty body returns nil data.
func (client *minifluxClient) request(method, path string, query url.Values, body any) (json.RawMessage, error) {
	endpoint := client.baseURL + "/v1" + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encoding request body: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, endpoint, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Auth-Token", client.apiKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling miniflux: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading miniflux response: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, &authError{
			Message: "miniflux rejected the API key",
			Fix:     "generate a new key in Miniflux under Settings > API Keys and update MINIFLUX_API_KEY or the rbw entry " + rbwEntry,
			Cause:   decodeAPIError(resp.StatusCode, data),
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, decodeAPIError(resp.StatusCode, data)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	return json.RawMessage(data), nil
}

func decodeAPIError(status int, data []byte) error {
	apiErr := &APIError{Status: status, Message: "miniflux request failed"}
	var body struct {
		ErrorMessage string `json:"error_message"`
	}
	if json.Unmarshal(data, &body) == nil && body.ErrorMessage != "" {
		apiErr.Details = body.ErrorMessage
	} else if trimmed := strings.TrimSpace(string(data)); trimmed != "" {
		// A proxy's 502 page is HTML; keep enough to recognize it, not the whole thing.
		if runes := []rune(trimmed); len(runes) > maxErrorDetails {
			trimmed = string(runes[:maxErrorDetails]) + "…"
		}
		apiErr.Details = trimmed
	}
	return apiErr
}

const maxErrorDetails = 300

func writeJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}
