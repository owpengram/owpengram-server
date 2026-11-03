// Package updatecheck tells an operator that a newer OwpenGram server release
// is published. It only looks: it never downloads or replaces anything, and an
// install made from a release archive (which has no git checkout to pull)
// is updated by hand from the release page it links to.
package updatecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"telesrv/internal/version"
)

// DefaultURL is the latest-release endpoint of the project's server repository.
const DefaultURL = "https://api.github.com/repos/owpengram/owpengram-server/releases/latest"

const (
	// Short: the overview shows the result, and an operator who just started
	// the panel should not have to wait a minute to learn an update exists.
	firstCheckDelay = 10 * time.Second
	checkInterval   = 24 * time.Hour
	retryInterval   = 6 * time.Hour
	maxBody         = 1 << 20
	maxNotes        = 2000
)

var tagRe = regexp.MustCompile(`^[Oo](\d+)$`)

// Status is what the admin panel shows. Available is false until a check has
// found a release newer than this build, and stays false when checking is off.
type Status struct {
	Enabled   bool      `json:"enabled"`
	Current   string    `json:"current"`
	Latest    string    `json:"latest,omitempty"`
	Available bool      `json:"available"`
	URL       string    `json:"url,omitempty"`
	Notes     string    `json:"notes,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

// Checker polls GitHub in the background and remembers the last answer.
type Checker struct {
	enabled bool
	current string
	url     string
	client  *http.Client

	mu     sync.Mutex
	status Status
}

// New builds a Checker for the running release. url "" means DefaultURL.
func New(enabled bool, current, url string) *Checker {
	if url == "" {
		url = DefaultURL
	}
	return &Checker{
		enabled: enabled,
		current: current,
		url:     url,
		client:  &http.Client{Timeout: 15 * time.Second},
		status:  Status{Enabled: enabled, Current: current},
	}
}

// Snapshot is the last known status; safe to call from request handlers.
func (c *Checker) Snapshot() Status {
	if c == nil {
		return Status{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

// Run checks once shortly after start, then daily, until ctx ends. It does
// nothing when checking is disabled or this build has no release number (a
// local build that would compare against nothing).
func (c *Checker) Run(ctx context.Context) {
	if !c.enabled || version.Number(c.current) == 0 {
		return
	}
	wait := firstCheckDelay
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if err := c.Check(ctx); err != nil {
			// advisory only: no network or a GitHub rate limit is not worth a log line per try
			wait = retryInterval
			continue
		}
		wait = checkInterval
	}
}

// Check asks GitHub once and records the answer.
func (c *Checker) Check(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "owpengram-server/"+c.current)
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("release check answered %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return err
	}
	status, err := evaluate(body, c.current)
	if err != nil {
		return err
	}
	status.Enabled = true
	status.CheckedAt = time.Now().UTC()
	c.mu.Lock()
	c.status = status
	c.mu.Unlock()
	return nil
}

// evaluate turns GitHub's release JSON into a Status for the running release.
func evaluate(body []byte, current string) (Status, error) {
	var release struct {
		TagName    string `json:"tag_name"`
		HTMLURL    string `json:"html_url"`
		Body       string `json:"body"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.Unmarshal(body, &release); err != nil {
		return Status{}, err
	}
	status := Status{Current: current}
	if release.Draft || release.Prerelease {
		return status, nil
	}
	match := tagRe.FindStringSubmatch(release.TagName)
	if match == nil {
		return status, nil
	}
	status.Latest = "O" + match[1]
	if version.Number(status.Latest) > version.Number(current) && strings.HasPrefix(release.HTMLURL, "https://") {
		status.Available = true
		status.URL = release.HTMLURL
		status.Notes = notes(release.Body)
	}
	return status, nil
}

func notes(body string) string {
	body = strings.TrimSpace(strings.ReplaceAll(body, "\r\n", "\n"))
	if len(body) > maxNotes {
		body = strings.TrimSpace(body[:maxNotes]) + "…"
	}
	return body
}
