package pom

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestPOMURL(t *testing.T) {
	g := GAV{"com.google.guava", "guava", "33.0.0"}
	want := "https://repo1.maven.org/maven2/com/google/guava/guava/33.0.0/guava-33.0.0.pom"
	if got := POMURL(DefaultRepoURL, g); got != want {
		t.Errorf("POMURL = %q want %q", got, want)
	}
}

func TestHTTPFetcher(t *testing.T) {
	var gotUA string
	mux := http.NewServeMux()
	mux.HandleFunc("/g/a/1/a-1.pom", func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(`<project><groupId>g</groupId><artifactId>a</artifactId><version>1</version></project>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := NewHTTPFetcher(srv.URL)
	p, err := f.Fetch(context.Background(), GAV{"g", "a", "1"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if p.ArtifactID != "a" {
		t.Errorf("parsed artifactId %q", p.ArtifactID)
	}
	if gotUA != DefaultUserAgent {
		t.Errorf("User-Agent = %q, want %q", gotUA, DefaultUserAgent)
	}

	if _, err := f.Fetch(context.Background(), GAV{"g", "a", "2"}); err == nil {
		t.Error("expected error for 404")
	}
}

type countingFetcher struct {
	calls atomic.Int32
	pom   *POM
}

func (c *countingFetcher) Fetch(_ context.Context, _ GAV) (*POM, error) {
	c.calls.Add(1)
	return c.pom, nil
}

func TestCachingFetcher(t *testing.T) {
	inner := &countingFetcher{pom: &POM{ArtifactID: "x"}}
	f := NewCachingFetcher(inner)
	g := GAV{"a", "b", "1"}
	for range 5 {
		p, err := f.Fetch(context.Background(), g)
		if err != nil || p.ArtifactID != "x" {
			t.Fatalf("Fetch: %v %v", p, err)
		}
	}
	if n := inner.calls.Load(); n != 1 {
		t.Errorf("inner fetcher called %d times, want 1", n)
	}
}

func TestDirFetcher(t *testing.T) {
	f := &DirFetcher{Dir: "testdata/poms"}
	p, err := f.Fetch(context.Background(), GAV{"org.slf4j", "slf4j-api", "2.0.13"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if p.ArtifactID != "slf4j-api" {
		t.Errorf("artifactId %q", p.ArtifactID)
	}
	if _, err := f.Fetch(context.Background(), GAV{"x", "y", "z"}); err == nil {
		t.Error("expected error for missing fixture")
	}
}

func TestNewHTTPFetcherDefault(t *testing.T) {
	if NewHTTPFetcher("").BaseURL != DefaultRepoURL {
		t.Error("empty baseURL should default to Maven Central")
	}
	if NewHTTPFetcher("http://x/").BaseURL != "http://x" {
		t.Error("trailing slash should be trimmed")
	}
}

func TestFixtureName(t *testing.T) {
	g := GAV{"org.foo", "bar", "1.0"}
	if FixtureName(g) != "org.foo_bar_1.0.pom" {
		t.Errorf("FixtureName = %q", FixtureName(g))
	}
}
