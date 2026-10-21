// Package version holds the nrp-mcp version and checks for newer GitHub releases.
package version

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Version is the release version.
const Version = "0.10.0"

// ReleasesAPI is the GitHub Releases API endpoint for nrp-mcp.
const ReleasesAPI = "https://api.github.com/repos/UCR-Research-Computing/nrp-mcp/releases/latest"

// CacheTTL is how long a cached update check is reused (once a day).
const CacheTTL = 24 * time.Hour

// CheckTimeout is the maximum time to wait when querying GitHub for the latest release.
const CheckTimeout = 3 * time.Second

// Checker queries the GitHub releases API and caches the result on disk.
type Checker struct {
	Current  string
	APIURL   string
	CacheDir string
	Timeout  time.Duration
	Now      func() time.Time
	HTTP     func(ctx context.Context, url string) ([]byte, error)
}

// DefaultChecker returns a Checker using the default state directory and 3s timeout.
func DefaultChecker(cacheDir string) *Checker {
	if cacheDir == "" {
		cacheDir = defaultStateDir()
	}
	return &Checker{
		Current:  Version,
		APIURL:   ReleasesAPI,
		CacheDir: cacheDir,
		Timeout:  CheckTimeout,
		Now:      time.Now,
	}
}

func defaultStateDir() string {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "nrp-mcp")
	}
	h, _ := os.UserHomeDir()
	if h == "" {
		return ""
	}
	return filepath.Join(h, ".local", "state", "nrp-mcp")
}

// Info describes the current and newest known release.
type Info struct {
	Current string `json:"current"`
	Latest  string `json:"latest,omitempty"`
	Newer   bool   `json:"newer,omitempty"`
	Cached  bool   `json:"cached,omitempty"`
	Notice  string `json:"notice,omitempty"`
}

type cacheRecord struct {
	Latest    string    `json:"latest"`
	CheckedAt time.Time `json:"checked_at"`
}

func (c *Checker) cachePath() string {
	if c.CacheDir == "" {
		return ""
	}
	return filepath.Join(c.CacheDir, "update-check.json")
}

func (c *Checker) now() time.Time {
	if c != nil && c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Checker) current() string {
	if c != nil && c.Current != "" {
		return strings.TrimPrefix(c.Current, "v")
	}
	return Version
}

// Check returns the newest release from cache (if checked within 24h and useCache is true)
// or from the GitHub releases API (3s timeout). When offline or rate-limited, it returns
// an Info with Latest="" and no error.
func (c *Checker) Check(ctx context.Context, useCache bool) Info {
	cur := c.current()
	info := Info{Current: cur}
	if c == nil {
		return info
	}
	now := c.now()
	if useCache {
		if rec, ok := c.readCache(); ok && !rec.CheckedAt.IsZero() && now.Sub(rec.CheckedAt) >= 0 && now.Sub(rec.CheckedAt) < CacheTTL {
			return buildInfo(cur, rec.Latest, true)
		}
	}
	latest, err := c.fetchLatest(ctx)
	if err != nil || latest == "" {
		return info
	}
	if useCache {
		c.writeCache(cacheRecord{Latest: latest, CheckedAt: now})
	}
	return buildInfo(cur, latest, false)
}

func buildInfo(cur, latest string, cached bool) Info {
	cleanLat := strings.TrimPrefix(strings.TrimSpace(latest), "v")
	info := Info{
		Current: cur,
		Latest:  cleanLat,
		Cached:  cached,
		Newer:   IsNewer(cur, cleanLat),
	}
	if info.Newer {
		info.Notice = fmt.Sprintf("a newer release v%s is available (you have v%s); see https://github.com/UCR-Research-Computing/nrp-mcp/releases/latest", cleanLat, cur)
	}
	return info
}

func (c *Checker) fetchLatest(ctx context.Context) (string, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = CheckTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	apiURL := c.APIURL
	if apiURL == "" {
		apiURL = ReleasesAPI
	}
	var b []byte
	var err error
	if c.HTTP != nil {
		b, err = c.HTTP(ctx, apiURL)
	} else {
		b, err = defaultHTTPGet(ctx, apiURL, timeout)
	}
	if err != nil {
		return "", err
	}
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(b, &rel); err != nil {
		return "", err
	}
	tag := strings.TrimSpace(rel.TagName)
	if !semverRe.MatchString(tag) {
		return "", fmt.Errorf("invalid tag %q", tag)
	}
	return strings.TrimPrefix(tag, "v"), nil
}

func defaultHTTPGet(ctx context.Context, url string, timeout time.Duration) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "nrp-mcp/"+Version)
	req.Header.Set("Accept", "application/vnd.github+json")
	hc := &http.Client{Timeout: timeout}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

func (c *Checker) readCache() (cacheRecord, bool) {
	p := c.cachePath()
	if p == "" {
		return cacheRecord{}, false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return cacheRecord{}, false
	}
	var r cacheRecord
	if err := json.Unmarshal(b, &r); err != nil || !semverRe.MatchString(r.Latest) {
		return cacheRecord{}, false
	}
	return r, true
}

func (c *Checker) writeCache(r cacheRecord) {
	p := c.cachePath()
	if p == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	b, err := json.Marshal(r)
	if err != nil {
		return
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, p)
}

var semverRe = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`)

// IsNewer reports whether candidate is strictly newer than current (major.minor.patch).
func IsNewer(current, candidate string) bool {
	ca, cb, cc, ok1 := parseSemver(current)
	da, db, dc, ok2 := parseSemver(candidate)
	if !ok1 || !ok2 {
		return false
	}
	if da != ca {
		return da > ca
	}
	if db != cb {
		return db > cb
	}
	return dc > cc
}

func parseSemver(v string) (int, int, int, bool) {
	m := semverRe.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return 0, 0, 0, false
	}
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	c, _ := strconv.Atoi(m[3])
	return a, b, c, true
}
