package pom

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

const (
	benchmarkParentDepth       = 16
	benchmarkBOMCount          = 6
	benchmarkManagedDepsPerBOM = 40
	benchmarkPropertyCount     = 128
	benchmarkManagedDepCount   = 256
	benchmarkArtifactCount     = 64
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

func BenchmarkResolveDeepParents(b *testing.B) {
	f, root := benchmarkDeepParents()
	ctx := context.Background()
	b.ResetTimer()
	for b.Loop() {
		if _, err := NewResolver(f).Resolve(ctx, root, Options{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkResolveImportedBOMs(b *testing.B) {
	f, root := benchmarkImportedBOMs()
	ctx := context.Background()
	b.ResetTimer()
	for b.Loop() {
		if _, err := NewResolver(f).Resolve(ctx, root, Options{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkResolveRepeatedProperties(b *testing.B) {
	f, root := benchmarkRepeatedProperties()
	ctx := context.Background()
	b.ResetTimer()
	for b.Loop() {
		if _, err := NewResolver(f).Resolve(ctx, root, Options{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkResolveDependencyManagement(b *testing.B) {
	f, root := benchmarkDependencyManagement()
	ctx := context.Background()
	b.ResetTimer()
	for b.Loop() {
		if _, err := NewResolver(f).Resolve(ctx, root, Options{}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkResolveManyArtifacts(b *testing.B) {
	f, roots := benchmarkManyArtifacts()
	ctx := context.Background()
	b.ResetTimer()
	for b.Loop() {
		r := NewResolver(f)
		for _, root := range roots {
			if _, err := r.Resolve(ctx, root, Options{}); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func benchmarkDeepParents() (memFetcher, GAV) {
	f := memFetcher{}
	var parent *Parent
	for i := range benchmarkParentDepth {
		id := "parent-" + strconv.Itoa(i)
		gav := GAV{GroupID: "org.example", ArtifactID: id, Version: "1"}
		p := &POM{
			GroupID:    gav.GroupID,
			ArtifactID: gav.ArtifactID,
			Version:    gav.Version,
			Parent:     parent,
			Properties: Properties{"shared.version": strconv.Itoa(i + 1)},
			Dependencies: []Dep{{
				GroupID: "org.example.lib", ArtifactID: "lib-" + strconv.Itoa(i),
			}},
			DependencyManagement: DepMgmt{Dependencies: []Dep{{
				GroupID: "org.example.lib", ArtifactID: "lib-" + strconv.Itoa(i), Version: "${shared.version}",
			}}},
		}
		f[gav] = p
		parent = &Parent{GroupID: gav.GroupID, ArtifactID: gav.ArtifactID, Version: gav.Version}
	}
	root := GAV{GroupID: "org.example", ArtifactID: "deep-app", Version: "1"}
	f[root] = &POM{GroupID: root.GroupID, ArtifactID: root.ArtifactID, Version: root.Version, Parent: parent}
	return f, root
}

func benchmarkImportedBOMs() (memFetcher, GAV) {
	f := memFetcher{}
	imports := make([]Dep, 0, benchmarkBOMCount)
	deps := make([]Dep, 0, benchmarkBOMCount*benchmarkManagedDepsPerBOM)
	for i := range benchmarkBOMCount {
		bom := GAV{GroupID: "org.example.bom", ArtifactID: "bom-" + strconv.Itoa(i), Version: "1"}
		managed := make([]Dep, 0, benchmarkManagedDepsPerBOM)
		for j := range benchmarkManagedDepsPerBOM {
			artifact := "lib-" + strconv.Itoa(i) + "-" + strconv.Itoa(j)
			managed = append(managed, Dep{GroupID: "org.example.lib", ArtifactID: artifact, Version: "1." + strconv.Itoa(j)})
			deps = append(deps, Dep{GroupID: "org.example.lib", ArtifactID: artifact})
		}
		f[bom] = &POM{GroupID: bom.GroupID, ArtifactID: bom.ArtifactID, Version: bom.Version, DependencyManagement: DepMgmt{Dependencies: managed}}
		imports = append(imports, Dep{GroupID: bom.GroupID, ArtifactID: bom.ArtifactID, Version: bom.Version, Type: "pom", Scope: scopeImport})
	}
	root := GAV{GroupID: "org.example", ArtifactID: "bom-app", Version: "1"}
	f[root] = &POM{
		GroupID: root.GroupID, ArtifactID: root.ArtifactID, Version: root.Version,
		DependencyManagement: DepMgmt{Dependencies: imports}, Dependencies: deps,
	}
	return f, root
}

func benchmarkRepeatedProperties() (memFetcher, GAV) {
	props := make(Properties, benchmarkPropertyCount)
	props["property.0"] = "1.0.0"
	for i := 1; i < benchmarkPropertyCount; i++ {
		props["property."+strconv.Itoa(i)] = "${property." + strconv.Itoa(i-1) + "}"
	}
	deps := make([]Dep, benchmarkPropertyCount)
	for i := range deps {
		deps[i] = Dep{GroupID: "org.example.lib", ArtifactID: "lib-" + strconv.Itoa(i), Version: "${property.127}"}
	}
	root := GAV{GroupID: "org.example", ArtifactID: "property-app", Version: "1"}
	f := memFetcher{root: {
		GroupID: root.GroupID, ArtifactID: root.ArtifactID, Version: root.Version,
		Properties: props, Dependencies: deps,
	}}
	return f, root
}

func benchmarkDependencyManagement() (memFetcher, GAV) {
	managed := make([]Dep, benchmarkManagedDepCount)
	deps := make([]Dep, benchmarkManagedDepCount)
	for i := range benchmarkManagedDepCount {
		artifact := "lib-" + strconv.Itoa(i)
		managed[i] = Dep{GroupID: "org.example.lib", ArtifactID: artifact, Version: "${shared.version}", Scope: "runtime"}
		deps[i] = Dep{GroupID: "org.example.lib", ArtifactID: artifact}
	}
	root := GAV{GroupID: "org.example", ArtifactID: "managed-app", Version: "1"}
	f := memFetcher{root: {
		GroupID: root.GroupID, ArtifactID: root.ArtifactID, Version: root.Version,
		Properties:           Properties{"shared.version": "2.0.0"},
		DependencyManagement: DepMgmt{Dependencies: managed}, Dependencies: deps,
	}}
	return f, root
}

func benchmarkManyArtifacts() (memFetcher, []GAV) {
	f, bomRoot := benchmarkImportedBOMs()
	parent := GAV{GroupID: "org.example", ArtifactID: "shared-parent", Version: "1"}
	f[parent] = &POM{
		GroupID: parent.GroupID, ArtifactID: parent.ArtifactID, Version: parent.Version,
		DependencyManagement: f[bomRoot].DependencyManagement,
	}
	roots := make([]GAV, benchmarkArtifactCount)
	for i := range roots {
		root := GAV{GroupID: "org.example", ArtifactID: "app-" + strconv.Itoa(i), Version: "1"}
		roots[i] = root
		f[root] = &POM{
			GroupID: root.GroupID, ArtifactID: root.ArtifactID, Version: root.Version,
			Parent:       &Parent{GroupID: parent.GroupID, ArtifactID: parent.ArtifactID, Version: parent.Version},
			Dependencies: []Dep{{GroupID: "org.example.lib", ArtifactID: "lib-0-0"}},
		}
	}
	return f, roots
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
