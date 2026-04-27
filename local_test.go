package pom

import (
	"context"
	"testing"
)

func TestResolveLocal(t *testing.T) {
	ep, err := ResolveLocal(context.Background(), "testdata/local/child/pom.xml", Options{})
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
	ep, err := ResolveLocal(context.Background(), "testdata/local/nested/pom.xml", Options{})
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
	if _, err := ResolveLocal(context.Background(), "testdata/local/nope/pom.xml", Options{}); err == nil {
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
