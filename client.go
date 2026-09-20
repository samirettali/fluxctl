package main

import (
	"bytes"
	"context"
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
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(baseURL, "#") {
		// Do not echo an invalid URL: it can contain credentials, including in
		// parse errors returned by net/url.
		return nil, notConfigured("miniflux URL is invalid: expected http(s)://host[/path] without credentials, query or fragment", nil)
	}
	return &minifluxClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		http: &http.Client{
			Timeout:       60 * time.Second,
			CheckRedirect: checkRedirect,
			Transport:     redirectTransport{http.DefaultTransport},
		},
	}, nil
}

// net/http includes the raw Location in errors when parsing a redirect URL.
// Validate it before the redirect machinery so malformed URLs cannot leak secrets.
// Leave valid responses and ordinary transport diagnostics unchanged.
type redirectTransport struct{ http.RoundTripper }

func (transport redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := transport.RoundTripper.RoundTrip(req)
	if err != nil {
		return resp, err
	}
	switch resp.StatusCode {
	case 301, 302, 303, 307, 308:
		if _, err := req.URL.Parse(resp.Header.Get("Location")); err != nil {
			resp.Body.Close()
			return nil, redirectError("invalid miniflux redirect location")
		}
	}
	return resp, nil
}

type redirectError string

func (err redirectError) Error() string { return string(err) }

// checkRedirect keeps the custom authentication header on its original origin and
// prevents a redirect from silently changing a mutation into a successful GET.
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return redirectError("stopped after 10 redirects")
	}
	original := via[0]
	if req.URL.Scheme != original.URL.Scheme || !strings.EqualFold(req.URL.Host, original.URL.Host) || req.URL.User != nil {
		return redirectError("refusing miniflux redirect to a different origin or URL credentials")
	}
	if req.Method != original.Method {
		return redirectError("refusing miniflux redirect that changes the request method")
	}
	return nil
}

// readVault returns the base URL and API key stored in the rbw entry. It never prompts:
// a locked vault is an error, since pinentry has no terminal to ask on from an agent.
func readVault() (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return readVaultContext(ctx)
}

func vaultCommand(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "rbw", args...)
	// Also bound waiting for inherited output pipes after the command exits.
	cmd.WaitDelay = time.Second
	return cmd
}

func readVaultContext(ctx context.Context) (string, string, error) {
	if _, err := exec.LookPath("rbw"); err != nil {
		return "", "", errors.New("rbw is not installed")
	}
	if err := vaultCommand(ctx, "unlocked").Run(); err != nil {
		if ctx.Err() != nil {
			return "", "", fmt.Errorf("checking rbw vault: %w", ctx.Err())
		}
		return "", "", errors.New("rbw is locked: run 'rbw unlock'")
	}
	raw, err := vaultCommand(ctx, "get", "--raw", rbwEntry).Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", "", fmt.Errorf("reading rbw entry: %w", ctx.Err())
		}
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
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encoding request body: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	data, err := client.requestBytes(method, path, query, reader, "application/json", "application/json")
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		if method == http.MethodGet {
			return nil, errors.New("decoding miniflux response: empty JSON response")
		}
		return nil, nil
	}
	if !json.Valid(data) {
		return nil, errors.New("decoding miniflux response: invalid JSON")
	}
	return redactResponse(json.RawMessage(data), body), nil
}

// requestBytes shares authentication, redirect and bounded error handling with JSON calls.
func (client *minifluxClient) requestBytes(method, path string, query url.Values, body io.Reader, contentType, accept string) ([]byte, error) {
	endpoint := client.baseURL + "/v1" + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Auth-Token", client.apiKey)
	req.Header.Set("Accept", accept)
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := client.http.Do(req)
	if err != nil {
		// net/http includes the request URL even when an accepted same-origin
		// redirect later fails in transport. That URL can contain credentials
		// echoed by the server. Retain the safe cause, never the URL wrapper.
		var redirectErr redirectError
		var urlErr *url.Error
		if errors.As(err, &redirectErr) {
			err = redirectErr
		} else if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return nil, fmt.Errorf("calling miniflux: %w", err)
	}
	defer resp.Body.Close()
	var responseBody io.Reader = resp.Body
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// Error details are only a diagnostic snippet; do not buffer an
		// arbitrarily large proxy error page. Success bodies remain uncapped
		// because --limit 0 deliberately retrieves all entries.
		responseBody = io.LimitReader(resp.Body, 16*1024)
	}
	data, readErr := io.ReadAll(responseBody)
	// Subscription and OPML failures may echo supplied credentials or entire
	// documents. Keep status/remedy but never surface their response diagnostics.
	if sensitiveRequest(method, path) && (resp.StatusCode < 200 || resp.StatusCode > 299) {
		data = nil
	}
	// Preserve the HTTP status (and authentication remedy) even when an error
	// response's body is truncated or unreadable.
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
	if readErr != nil {
		return nil, fmt.Errorf("reading miniflux response: %w", readErr)
	}
	return data, nil
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
