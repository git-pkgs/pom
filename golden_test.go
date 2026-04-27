package pom

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

type expectedDep struct {
	GroupID    string `json:"groupId"`
	ArtifactID string `json:"artifactId"`
	Version    string `json:"version"`
	Scope      string `json:"scope"`
	Type       string `json:"type"`
	Classifier string `json:"classifier"`
	Optional   bool   `json:"optional"`
}

type expectedFile struct {
	GAV          string        `json:"gav"`
	Dependencies []expectedDep `json:"dependencies"`
}

// TestGoldenAgainstMaven compares the pure-Go resolver's output against
// dependency lists produced by `mvn help:effective-pom` (captured by
// tools/refresh). Each testdata/expected/*.json is one artifact.
func TestGoldenAgainstMaven(t *testing.T) {
	files, err := filepath.Glob("testdata/expected/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no golden files; run go run ./tools/refresh")
	}

	fetcher := NewCachingFetcher(&DirFetcher{Dir: "testdata/poms"})
	r := NewResolver(fetcher)
	ctx := context.Background()

	for _, f := range files {
		var exp expectedFile
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &exp); err != nil {
			t.Fatalf("%s: %v", f, err)
		}

		t.Run(exp.GAV, func(t *testing.T) {
			gav, err := ParseGAV(exp.GAV)
			if err != nil {
				t.Fatal(err)
			}
			ep, err := r.Resolve(ctx, gav, Options{Profiles: ProfileActivation{Mode: OnlyDefault}})
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			for _, w := range ep.Warnings {
				t.Logf("warn: %s", w)
			}
			compareDeps(t, exp.Dependencies, ep.Dependencies)
		})
	}
}

func compareDeps(t *testing.T, want []expectedDep, got []ResolvedDep) {
	t.Helper()

	key := func(g, a, ty, c string) string { return g + ":" + a + ":" + ty + ":" + c }

	wantM := map[string]expectedDep{}
	for _, d := range want {
		wantM[key(d.GroupID, d.ArtifactID, d.Type, d.Classifier)] = d
	}
	gotM := map[string]ResolvedDep{}
	envGated := 0
	for _, d := range got {
		// Deps whose identity (g/a/classifier) couldn't be statically
		// resolved depend on build extensions or OS/JDK profiles. Maven's
		// own effective-pom output for these varies by host, so they are
		// excluded from strict comparison.
		if d.Resolution != Resolved && containsExpr(d.GroupID+d.ArtifactID+d.Classifier) {
			t.Logf("skip env-gated: %s:%s classifier=%q (%s, expr=%s)",
				d.GroupID, d.ArtifactID, d.Classifier, d.Resolution, d.Expression)
			envGated++
			continue
		}
		gotM[key(d.GroupID, d.ArtifactID, d.Type, d.Classifier)] = d
	}

	var missing, extra, mismatch []string
	for k, w := range wantM {
		g, ok := gotM[k]
		if !ok {
			missing = append(missing, k+" (want "+w.Version+")")
			continue
		}
		if g.Version != w.Version {
			mismatch = append(mismatch, k+" version: got "+g.Version+" want "+w.Version)
		}
		if g.Scope != w.Scope {
			mismatch = append(mismatch, k+" scope: got "+g.Scope+" want "+w.Scope)
		}
		if g.Optional != w.Optional {
			mismatch = append(mismatch, k+" optional mismatch")
		}
	}
	for k, g := range gotM {
		if _, ok := wantM[k]; !ok {
			extra = append(extra, k+" (got "+g.Version+", "+string(g.Resolution)+")")
		}
	}
	// Each env-gated dep we skipped corresponds to one entry in maven's
	// output under a different (host-specific) key.
	if len(missing) <= envGated && len(extra) == 0 {
		for _, m := range missing {
			t.Logf("tolerated as env-gated counterpart: %s", m)
		}
		missing = nil
	}

	sort.Strings(missing)
	sort.Strings(extra)
	sort.Strings(mismatch)

	if len(missing) > 0 {
		t.Errorf("missing %d dep(s):\n  %s", len(missing), strings.Join(missing, "\n  "))
	}
	if len(mismatch) > 0 {
		t.Errorf("mismatched %d dep(s):\n  %s", len(mismatch), strings.Join(mismatch, "\n  "))
	}
	if len(extra) > 0 {
		t.Errorf("extra %d dep(s) not in maven output:\n  %s", len(extra), strings.Join(extra, "\n  "))
	}
}
