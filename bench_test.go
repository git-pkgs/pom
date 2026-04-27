package pom

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// memFetcher preloads every fixture POM into memory so benchmarks measure
// resolution work, not disk I/O.
type memFetcher map[GAV]*POM

func loadFixtures(tb testing.TB) memFetcher {
	tb.Helper()
	files, err := filepath.Glob("testdata/poms/*.pom")
	if err != nil {
		tb.Fatal(err)
	}
	m := memFetcher{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			tb.Fatal(err)
		}
		p, err := ParsePOM(data)
		if err != nil {
			tb.Fatalf("%s: %v", f, err)
		}
		m[p.EffectiveGAV()] = p
	}
	return m
}

func (m memFetcher) Fetch(_ context.Context, g GAV) (*POM, error) {
	p, ok := m[g]
	if !ok {
		return nil, os.ErrNotExist
	}
	return p, nil
}

func BenchmarkInterpolate(b *testing.B) {
	props := map[string]string{
		"project.version": "2.17.2",
		"project.groupId": "com.fasterxml.jackson.core",
		"jackson.version": "${project.version}",
		"lib.version":     "1.0",
	}
	cases := []struct {
		name string
		in   string
	}{
		{"noexpr", "1.2.3"},
		{"single", "${project.version}"},
		{"chained", "${jackson.version}"},
		{"miss", "${not.defined}"},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			for b.Loop() {
				_ = interpolate(c.in, props)
			}
		})
	}
}

func BenchmarkParsePOM(b *testing.B) {
	cases := []struct {
		name string
		path string
	}{
		{"small", "testdata/poms/org.slf4j_slf4j-api_2.0.13.pom"},
		{"large", "testdata/poms/io.netty_netty-parent_4.1.112.Final.pom"},
	}
	for _, c := range cases {
		data, err := os.ReadFile(c.path)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(c.name, func(b *testing.B) {
			b.SetBytes(int64(len(data)))
			for b.Loop() {
				if _, err := ParsePOM(data); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

var resolveCases = []string{
	"org.slf4j:slf4j-api:2.0.13",
	"com.fasterxml.jackson.core:jackson-databind:2.17.2",
	"io.netty:netty-handler:4.1.112.Final",
	"org.apache.logging.log4j:log4j-core:2.23.1",
}

func BenchmarkResolve(b *testing.B) {
	f := loadFixtures(b)
	ctx := context.Background()
	for _, coord := range resolveCases {
		gav, _ := ParseGAV(coord)
		b.Run(gav.ArtifactID, func(b *testing.B) {
			for b.Loop() {
				r := NewResolver(f)
				if _, err := r.Resolve(ctx, gav, Options{}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkResolveCorpus resolves every golden artifact with a single
// resolver so shared parents and BOMs hit the memo cache. This approximates
// throughput when scanning a corpus.
func BenchmarkResolveCorpus(b *testing.B) {
	f := loadFixtures(b)
	ctx := context.Background()
	files, _ := filepath.Glob("testdata/expected/*.json")
	gavs := make([]GAV, 0, len(files))
	for _, fp := range files {
		base := filepath.Base(fp)
		// org.foo_bar_1.0.json -> org.foo:bar:1.0
		parts := splitFixtureName(base[:len(base)-len(".json")])
		gavs = append(gavs, GAV{parts[0], parts[1], parts[2]})
	}
	b.ResetTimer()
	for b.Loop() {
		r := NewResolver(f)
		for _, g := range gavs {
			if _, err := r.Resolve(ctx, g, Options{}); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func splitFixtureName(s string) [3]string {
	var out [3]string
	first := -1
	for i, c := range s {
		if c == '_' {
			if first < 0 {
				first = i
			} else {
				out[0], out[1], out[2] = s[:first], s[first+1:i], s[i+1:]
				return out
			}
		}
	}
	return out
}
