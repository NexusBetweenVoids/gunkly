// Package updates checks for a newer Gunkly release. The only request is a GET
// of the latest-release document of the public repository, through the
// egress-guarded client, with a generic User-Agent and no instance data (no
// version, URL, user or counts): the server learns only that someone asked.
// The admin can switch the check off; the result is cached in the settings
// table and the check runs at most every 12 hours.
package updates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// Fixed parameters.
const (
	// LatestReleaseURL is the GitHub Releases API document of the latest release.
	LatestReleaseURL = "https://api.github.com/repos/NexusBetweenVoids/gunkly/releases/latest"
	UserAgent        = "gunkly-update-check"
	Interval         = 12 * time.Hour
	// MaxChangelog caps the stored release notes, in characters.
	MaxChangelog = 8000
	maxTagLen    = 64
	pollEvery    = 30 * time.Minute
	firstDelay   = 2 * time.Minute
)

var tagRE = regexp.MustCompile(`^v?\d{1,6}\.\d{1,6}\.\d{1,6}(-[0-9A-Za-z.-]{1,40})?(\+[0-9A-Za-z.-]{1,40})?$`)

// Checker fetches and caches the latest release.
type Checker struct {
	Store  *store.Store
	Client *http.Client     // must be egress-guarded
	URL    string           // "" = LatestReleaseURL
	Now    func() time.Time // nil = time.Now
	Log    *slog.Logger     // nil = slog.Default()
}

func (c *Checker) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Run checks shortly after start and then whenever the cache is older than
// Interval, until ctx ends. Safe on every instance: the cache is shared, so
// instances mostly find it fresh.
func (c *Checker) Run(ctx context.Context) {
	log := c.Log
	if log == nil {
		log = slog.Default()
	}
	t := time.NewTimer(firstDelay)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if err := c.CheckIfDue(ctx); err != nil && ctx.Err() == nil {
			log.Warn("update check failed", "err", err)
		}
		t.Reset(pollEvery)
	}
}

// CheckIfDue asks GitHub unless the check is disabled or ran within Interval.
// A failed attempt is remembered too, so an outage is not retried faster than
// a success would be.
func (c *Checker) CheckIfDue(ctx context.Context) error {
	st, err := c.Store.InstanceSettings(ctx)
	if err != nil || !st.UpdateCheck {
		return err
	}
	cache, err := c.Store.UpdateCache(ctx)
	if err != nil {
		return err
	}
	now := c.now()
	last := cache.AttemptedAt
	if cache.CheckedAt.After(last) {
		last = cache.CheckedAt
	}
	if !last.IsZero() && now.Sub(last) < Interval && !last.After(now) {
		return nil
	}
	cache.AttemptedAt = now
	latest, notes, err := c.fetch(ctx)
	if err == nil {
		cache.Latest, cache.Changelog, cache.CheckedAt = latest, notes, now
	}
	if serr := c.Store.SetUpdateCache(ctx, cache); serr != nil {
		return errors.Join(err, serr)
	}
	return err
}

func (c *Checker) fetch(ctx context.Context) (tag, notes string, err error) {
	u := c.URL
	if u == "" {
		u = LatestReleaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.Client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("updates: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return "", "", fmt.Errorf("updates: status %d", resp.StatusCode)
	}
	var rel struct {
		TagName    string `json:"tag_name"`
		Body       string `json:"body"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", "", fmt.Errorf("updates: bad response: %w", err)
	}
	if rel.Draft || len(rel.TagName) > maxTagLen || !tagRE.MatchString(rel.TagName) {
		return "", "", errors.New("updates: unusable release")
	}
	return rel.TagName, PlainText(rel.Body), nil
}

// PlainText makes release notes safe to show as text: control characters other
// than newline and tab are dropped, line endings normalised and the length
// capped. Callers must still render it as text, never as HTML.
func PlainText(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	var b strings.Builder
	n := 0
	for _, r := range strings.ToValidUTF8(s, "") {
		if r < ' ' && r != '\n' && r != '\t' || r == 0x7f {
			continue
		}
		if n++; n > MaxChangelog {
			b.WriteString("…")
			break
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// Newer reports whether release latest is newer than the running version.
// Versions that do not parse (a "dev" build) are never reported as outdated.
func Newer(running, latest string) bool {
	r, ok1 := parse(running)
	l, ok2 := parse(latest)
	if !ok1 || !ok2 {
		return false
	}
	for i := range r.num {
		if l.num[i] != r.num[i] {
			return l.num[i] > r.num[i]
		}
	}
	return r.pre != "" && l.pre == "" // 1.2.0 is newer than 1.2.0-rc1
}

type version struct {
	num [3]int
	pre string
}

func parse(s string) (version, bool) {
	s = strings.TrimPrefix(s, "v")
	s, _, _ = strings.Cut(s, "+")
	core, pre, _ := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return version{}, false
	}
	v := version{pre: pre}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return version{}, false
		}
		v.num[i] = n
	}
	return v, true
}
