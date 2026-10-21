package version

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestIsNewer(t *testing.T) {
	cases := []struct {
		cur, lat string
		want     bool
	}{
		{"0.10.0", "1.0.0", true},
		{"v0.10.0", "v0.10.1", true},
		{"1.0.0", "1.0.0", false},
		{"1.0.0", "0.10.0", false},
		{"1.0.0", "junk", false},
	}
	for _, tc := range cases {
		if got := IsNewer(tc.cur, tc.lat); got != tc.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", tc.cur, tc.lat, got, tc.want)
		}
	}
}

func TestCheckerCachesOnceADayAndSkipsOffline(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v1.1.0"}`))
	}))
	defer srv.Close()

	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	c := &Checker{
		Current:  "0.10.0",
		APIURL:   srv.URL,
		CacheDir: t.TempDir(),
		Timeout:  3 * time.Second,
		Now:      func() time.Time { return now },
	}

	// First check hits the API and writes the cache.
	info := c.Check(context.Background(), true)
	if !info.Newer || info.Latest != "1.1.0" || info.Cached || !strings.Contains(info.Notice, "v1.1.0") {
		t.Fatalf("first check: %+v", info)
	}
	if hits.Load() != 1 {
		t.Fatalf("expected 1 HTTP hit, got %d", hits.Load())
	}

	// Second check 2 hours later reuses the cached value without hitting the API.
	now = now.Add(2 * time.Hour)
	info2 := c.Check(context.Background(), true)
	if !info2.Newer || info2.Latest != "1.1.0" || !info2.Cached {
		t.Fatalf("cached check: %+v", info2)
	}
	if hits.Load() != 1 {
		t.Fatalf("cache should have prevented second HTTP hit, got %d", hits.Load())
	}

	// After 25 hours, cache expires and API is queried again.
	now = now.Add(25 * time.Hour)
	info3 := c.Check(context.Background(), true)
	if !info3.Newer || info3.Cached || hits.Load() != 2 {
		t.Fatalf("post-TTL check: %+v hits=%d", info3, hits.Load())
	}

	// Offline / error is skipped cleanly.
	offline := &Checker{
		Current: "0.10.0",
		HTTP: func(context.Context, string) ([]byte, error) {
			return nil, context.DeadlineExceeded
		},
	}
	offInfo := offline.Check(context.Background(), false)
	if offInfo.Latest != "" || offInfo.Newer || offInfo.Notice != "" {
		t.Fatalf("offline check must be skipped cleanly: %+v", offInfo)
	}
}
