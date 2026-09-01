package updatecdn

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Resolver interface {
	Resolve(ctx context.Context, req ResolveRequest) (*ResolvedUpdate, error)
}

type Client struct {
	baseURL string
	http    *http.Client
}

func NewClient(baseURL string, timeout time.Duration) (*Client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse update service URL: %w", err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("update service URL must be an HTTP(S) base URL without credentials, query, or fragment")
	}
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return &Client{baseURL: baseURL, http: &http.Client{Timeout: timeout}}, nil
}

func (c *Client) Resolve(ctx context.Context, request ResolveRequest) (*ResolvedUpdate, error) {
	u, err := url.Parse(c.baseURL + "/v1/resolve")
	if err != nil {
		return nil, err
	}
	query := u.Query()
	query.Set("platform", request.Platform)
	query.Set("channel", request.Channel)
	query.Set("version", request.Version)
	query.Set("source", request.Source)
	query.Set("lang_code", request.LangCode)
	u.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build update resolve request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("resolve application update: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("resolve application update: HTTP %d", resp.StatusCode)
	}
	var resolved ResolvedUpdate
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 256<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&resolved); err != nil {
		return nil, fmt.Errorf("decode application update: %w", err)
	}
	if resolved.ID <= 0 || strings.TrimSpace(resolved.Version) == "" || strings.TrimSpace(resolved.Text) == "" {
		return nil, fmt.Errorf("resolve application update: incomplete response")
	}
	return &resolved, nil
}
