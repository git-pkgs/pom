package pom

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// DefaultRepoURL is the canonical Maven Central repository.
const DefaultRepoURL = "https://repo1.maven.org/maven2"

// DefaultUserAgent identifies this library to upstream repositories.
var DefaultUserAgent = "git-pkgs-pom/" + Version + " (+https://github.com/git-pkgs/pom)"

// HTTPFetcher fetches POMs from a Maven repository layout over HTTP.
type HTTPFetcher struct {
	BaseURL   string
	UserAgent string
	Client    *http.Client
}

// NewHTTPFetcher returns a fetcher for baseURL, defaulting to Maven Central.
func NewHTTPFetcher(baseURL string) *HTTPFetcher {
	if baseURL == "" {
		baseURL = DefaultRepoURL
	}
	return &HTTPFetcher{
		BaseURL:   strings.TrimSuffix(baseURL, "/"),
		UserAgent: DefaultUserAgent,
		Client:    http.DefaultClient,
	}
}

func (f *HTTPFetcher) Fetch(ctx context.Context, gav GAV) (*POM, error) {
	data, err := f.FetchBytes(ctx, gav)
	if err != nil {
		return nil, err
	}
	return ParsePOM(data)
}

// FetchBytes returns the raw POM bytes. Exposed so the refresh tool can
// persist fixtures without re-serialising.
func (f *HTTPFetcher) FetchBytes(ctx context.Context, gav GAV) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, POMURL(f.BaseURL, gav), nil)
	if err != nil {
		return nil, err
	}
	if f.UserAgent != "" {
		req.Header.Set("User-Agent", f.UserAgent)
	}
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d for %s", resp.StatusCode, req.URL)
	}
	return io.ReadAll(resp.Body)
}

// POMURL builds the repository URL for gav's POM under base.
func POMURL(base string, gav GAV) string {
	return fmt.Sprintf("%s/%s/%s/%s/%s-%s.pom",
		strings.TrimSuffix(base, "/"),
		strings.ReplaceAll(gav.GroupID, ".", "/"),
		gav.ArtifactID, gav.Version, gav.ArtifactID, gav.Version)
}

// DirFetcher reads POMs from a flat directory of files named
// "<groupId>_<artifactId>_<version>.pom". Used by tests so resolution
// runs entirely offline.
type DirFetcher struct {
	Dir string
}

func (f *DirFetcher) Fetch(_ context.Context, gav GAV) (*POM, error) {
	path := filepath.Join(f.Dir, FixtureName(gav))
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParsePOM(data)
}

// FixtureName returns the on-disk filename DirFetcher expects for gav.
func FixtureName(gav GAV) string {
	return fmt.Sprintf("%s_%s_%s.pom", gav.GroupID, gav.ArtifactID, gav.Version)
}

// CachingFetcher wraps another Fetcher and memoises results by GAV.
// Safe for concurrent use.
type CachingFetcher struct {
	Inner Fetcher

	mu    sync.RWMutex
	cache map[GAV]*POM
}

// NewCachingFetcher wraps inner with an in-memory cache.
func NewCachingFetcher(inner Fetcher) *CachingFetcher {
	return &CachingFetcher{Inner: inner, cache: map[GAV]*POM{}}
}

func (f *CachingFetcher) Fetch(ctx context.Context, gav GAV) (*POM, error) {
	f.mu.RLock()
	p, ok := f.cache[gav]
	f.mu.RUnlock()
	if ok {
		return p, nil
	}
	p, err := f.Inner.Fetch(ctx, gav)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.cache[gav] = p
	f.mu.Unlock()
	return p, nil
}
