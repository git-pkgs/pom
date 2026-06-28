package pom

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveLocal(t *testing.T) {
	ep, err := ResolveLocal(context.Background(), "testdata/local/child/pom.xml", "testdata/local", Options{})
	if err != nil {
		t.Fatalf("ResolveLocal: %v", err)
	}
	if ep.GAV != (GAV{"org.example", "child", "1.0-SNAPSHOT"}) {
		t.Errorf("gav: %v", ep.GAV)
	}
	if len(ep.Parents) != 1 || ep.Parents[0].ArtifactID != "parent" {
		t.Errorf("parents: %v", ep.Parents)
	}
	if ep.Description != "root" {
		t.Errorf("inherited description: %q", ep.Description)
	}

	want := map[string]string{
		"org.example:sibling":      "1.0-SNAPSHOT",
		"org.openjdk.jmh:jmh-core": "1.37",
		"org.lib:lib":              "2.5",
	}
	for _, d := range ep.Dependencies {
		k := d.GroupID + ":" + d.ArtifactID
		if want[k] != d.Version {
			t.Errorf("%s: got %q want %q (resolution=%s)", k, d.Version, want[k], d.Resolution)
		}
		if d.Resolution != Resolved {
			t.Errorf("%s: resolution=%s", k, d.Resolution)
		}
	}
	if len(ep.Warnings) != 0 {
		t.Errorf("warnings: %v", ep.Warnings)
	}
}

func TestResolveLocalEmptyRelativePath(t *testing.T) {
	ep, err := ResolveLocal(context.Background(), "testdata/local/nested/pom.xml", "testdata/local", Options{})
	if err != nil {
		t.Fatalf("ResolveLocal: %v", err)
	}
	if len(ep.Parents) != 0 {
		t.Errorf("explicit <relativePath/> should skip local lookup, got parents: %v", ep.Parents)
	}
	if len(ep.Warnings) == 0 {
		t.Error("expected unresolved-parent warning")
	}
	d := ep.Dependencies[0]
	if d.Resolution != UnresolvedParent {
		t.Errorf("dep should be tagged unresolved_parent, got %s", d.Resolution)
	}
}

func TestResolveLocalMissingFile(t *testing.T) {
	if _, err := ResolveLocal(context.Background(), "testdata/local/nope/pom.xml", "testdata/local", Options{}); err == nil {
		t.Error("expected error for missing root file")
	}
}

func TestParentLocalPath(t *testing.T) {
	empty := ""
	custom := "../../parent/pom.xml"
	tests := []struct {
		rp   *string
		want string
	}{
		{nil, "../pom.xml"},
		{&empty, ""},
		{&custom, "../../parent/pom.xml"},
	}
	for _, tt := range tests {
		p := &Parent{RelativePath: tt.rp}
		if got := p.LocalPath(); got != tt.want {
			t.Errorf("LocalPath(%v) = %q want %q", tt.rp, got, tt.want)
		}
	}
}

func TestLocalFetcherRejectsAbsoluteRelativePath(t *testing.T) {
	tmp := t.TempDir()
	childDir := filepath.Join(tmp, "child")
	_ = os.MkdirAll(childDir, 0o755)

	targetPOM := filepath.Join(tmp, "target", "pom.xml")
	_ = os.MkdirAll(filepath.Dir(targetPOM), 0o755)
	_ = os.WriteFile(targetPOM, []byte(`<project><groupId>org.evil</groupId><artifactId>evil</artifactId><version>1.0</version></project>`), 0o644)

	absPath := targetPOM
	child := &POM{
		GroupID:    "org.example",
		ArtifactID: "child",
		Version:    "1.0",
		Parent:     &Parent{GroupID: "org.evil", ArtifactID: "evil", Version: "1.0", RelativePath: &absPath},
	}

	f := NewLocalFetcherFrom(child, childDir, tmp)
	_, err := f.Fetch(context.Background(), GAV{"org.evil", "evil", "1.0"})
	if err == nil {
		t.Error("expected error: absolute relativePath should be rejected")
	}
}

func TestLocalFetcherRejectsSymlink(t *testing.T) {
	tmp := t.TempDir()
	childDir := filepath.Join(tmp, "child")
	_ = os.MkdirAll(childDir, 0o755)

	// Create a target POM outside the project tree
	outsideDir := filepath.Join(tmp, "outside")
	_ = os.MkdirAll(outsideDir, 0o755)
	_ = os.WriteFile(filepath.Join(outsideDir, "pom.xml"), []byte(`<project><groupId>org.evil</groupId><artifactId>evil</artifactId><version>1.0</version></project>`), 0o644)

	// Create symlink from child/parent -> outside
	symlink := filepath.Join(childDir, "parent")
	if err := os.Symlink(outsideDir, symlink); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}

	rel := "parent"
	child := &POM{
		GroupID:    "org.example",
		ArtifactID: "child",
		Version:    "1.0",
		Parent:     &Parent{GroupID: "org.evil", ArtifactID: "evil", Version: "1.0", RelativePath: &rel},
	}

	f := NewLocalFetcherFrom(child, childDir, tmp)
	_, err := f.Fetch(context.Background(), GAV{"org.evil", "evil", "1.0"})
	if err == nil {
		t.Error("expected error: symlink traversal should be rejected")
	}
}

func TestLocalFetcherRejectsRootEscape(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	childDir := filepath.Join(repo, "child")
	_ = os.MkdirAll(childDir, 0o755)

	outside := filepath.Join(tmp, "outside", "pom.xml")
	_ = os.MkdirAll(filepath.Dir(outside), 0o755)
	_ = os.WriteFile(outside, []byte(`<project><groupId>org.evil</groupId><artifactId>evil</artifactId><version>1.0</version></project>`), 0o644)

	rel := "../../outside/pom.xml"
	child := &POM{
		GroupID:    "org.example",
		ArtifactID: "child",
		Version:    "1.0",
		Parent:     &Parent{GroupID: "org.evil", ArtifactID: "evil", Version: "1.0", RelativePath: &rel},
	}

	f := NewLocalFetcherFrom(child, childDir, repo)
	if _, err := f.Fetch(context.Background(), GAV{"org.evil", "evil", "1.0"}); err == nil {
		t.Error("expected error: relativePath escaping fsRoot should be rejected")
	}
}

func TestLocalFetcherRejectsRootEscapePrefixSibling(t *testing.T) {
	// Guard against the classic strings.HasPrefix bug where /tmp/repo-evil
	// is treated as inside /tmp/repo because one is a string prefix of the
	// other.
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	sibling := filepath.Join(tmp, "repo-evil")
	childDir := filepath.Join(repo, "child")
	_ = os.MkdirAll(childDir, 0o755)
	_ = os.MkdirAll(sibling, 0o755)
	_ = os.WriteFile(filepath.Join(sibling, "pom.xml"), []byte(`<project><groupId>org.evil</groupId><artifactId>evil</artifactId><version>1.0</version></project>`), 0o644)

	rel := "../../repo-evil/pom.xml"
	child := &POM{
		GroupID:    "org.example",
		ArtifactID: "child",
		Version:    "1.0",
		Parent:     &Parent{GroupID: "org.evil", ArtifactID: "evil", Version: "1.0", RelativePath: &rel},
	}

	f := NewLocalFetcherFrom(child, childDir, repo)
	if _, err := f.Fetch(context.Background(), GAV{"org.evil", "evil", "1.0"}); err == nil {
		t.Error("expected error: sibling directory with shared prefix should be rejected")
	}
}

func TestLocalFetcherEmptyRootSkipsWalk(t *testing.T) {
	ep, err := ResolveLocal(context.Background(), "testdata/local/child/pom.xml", "", Options{})
	if err != nil {
		t.Fatalf("ResolveLocal: %v", err)
	}
	if len(ep.Parents) != 0 {
		t.Errorf("empty fsRoot should disable parent walk, got parents: %v", ep.Parents)
	}
	if len(ep.Warnings) == 0 {
		t.Error("expected unresolved-parent warning when walk is disabled")
	}
}

func TestParsePOMRejectsOversize(t *testing.T) {
	old := MaxPOMBytes
	MaxPOMBytes = 1024
	t.Cleanup(func() { MaxPOMBytes = old })

	data := append([]byte("<project>"), bytes.Repeat([]byte("x"), 2000)...)
	if _, err := ParsePOM(data); !errors.Is(err, ErrPOMTooLarge) {
		t.Errorf("expected ErrPOMTooLarge, got %v", err)
	}
}

func TestReadPOMFileRejectsOversize(t *testing.T) {
	old := MaxPOMBytes
	MaxPOMBytes = 1024
	t.Cleanup(func() { MaxPOMBytes = old })

	tmp := t.TempDir()
	path := filepath.Join(tmp, "pom.xml")
	_ = os.WriteFile(path, bytes.Repeat([]byte("x"), 2000), 0o644)

	if _, err := readPOMFile(path); !errors.Is(err, ErrPOMTooLarge) {
		t.Errorf("expected ErrPOMTooLarge from readPOMFile, got %v", err)
	}
}
