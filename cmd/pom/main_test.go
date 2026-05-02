package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const childPOM = `<project>
	<parent><groupId>org.x</groupId><artifactId>parent</artifactId><version>1</version></parent>
	<artifactId>child</artifactId>
	<dependencies>
		<dependency><groupId>a</groupId><artifactId>b</artifactId><version>${lib.version}</version></dependency>
	</dependencies>
</project>`

const parentPOM = `<project>
	<groupId>org.x</groupId><artifactId>parent</artifactId><version>1</version>
	<description>parent desc</description>
	<licenses><license><name>MIT</name></license></licenses>
	<properties><lib.version>2.0</lib.version></properties>
</project>`

func newServer() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/org/x/parent/1/parent-1.pom", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(parentPOM))
	})
	mux.HandleFunc("/org/x/child/1/child-1.pom", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(childPOM))
	})
	return httptest.NewServer(mux)
}

func decode(t *testing.T, b []byte) output {
	t.Helper()
	var o output
	if err := json.Unmarshal(b, &o); err != nil {
		t.Fatalf("decode: %v\n%s", err, b)
	}
	return o
}

func TestRunCoord(t *testing.T) {
	srv := newServer()
	defer srv.Close()

	var out bytes.Buffer
	err := run([]string{"-repo", srv.URL, "org.x:child:1"}, nil, &out)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	o := decode(t, out.Bytes())
	if o.GAV != "org.x:child:1" {
		t.Errorf("gav: %q", o.GAV)
	}
	if o.Description != "parent desc" {
		t.Errorf("inherited description: %q", o.Description)
	}
	if len(o.Licenses) != 1 || o.Licenses[0].Name != "MIT" {
		t.Errorf("licenses: %+v", o.Licenses)
	}
	if len(o.Dependencies) != 1 || o.Dependencies[0].Version != "2.0" || o.Dependencies[0].Resolution != "resolved" {
		t.Errorf("deps: %+v", o.Dependencies)
	}
	if len(o.Parents) != 1 || o.Parents[0] != "org.x:parent:1" {
		t.Errorf("parents: %v", o.Parents)
	}
}

func TestRunStdin(t *testing.T) {
	srv := newServer()
	defer srv.Close()

	var out bytes.Buffer
	err := run([]string{"-repo", srv.URL, "-f", "-"}, strings.NewReader(childPOM), &out)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	o := decode(t, out.Bytes())
	if len(o.Dependencies) != 1 || o.Dependencies[0].Version != "2.0" {
		t.Errorf("deps via stdin: %+v", o.Dependencies)
	}
}

func TestRunFile(t *testing.T) {
	var out bytes.Buffer
	err := run([]string{"-f", "../../testdata/poms/org.reflections_reflections_0.10.2.pom"}, nil, &out)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	o := decode(t, out.Bytes())
	if o.GAV != "org.reflections:reflections:0.10.2" {
		t.Errorf("gav: %q", o.GAV)
	}
	found := false
	for _, d := range o.Dependencies {
		if d.ArtifactID == "javassist" && d.Version == "3.28.0-GA" {
			found = true
		}
	}
	if !found {
		t.Errorf("javassist not resolved: %+v", o.Dependencies)
	}
}

func TestRunXML(t *testing.T) {
	srv := newServer()
	defer srv.Close()

	var out bytes.Buffer
	if err := run([]string{"-repo", srv.URL, "-xml", "-f", "-"}, strings.NewReader(childPOM), &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	s := out.String()
	for _, want := range []string{
		`<?xml version="1.0" encoding="UTF-8"?>`,
		`<groupId>org.x</groupId>`,
		`<artifactId>child</artifactId>`,
		`<version>1</version>`,
		`<description>parent desc</description>`,
		`<name>MIT</name>`,
		`<lib.version>2.0</lib.version>`,
		`<artifactId>b</artifactId>`,
		`<version>2.0</version>`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("xml output missing %q\n%s", want, s)
		}
	}
	if strings.Contains(s, "<parent>") {
		t.Errorf("xml output should not contain <parent> (already merged)\n%s", s)
	}
}

func TestRunRelocate(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/old/a/1/a-1.pom", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<project><groupId>old</groupId><artifactId>a</artifactId><version>1</version>
			<distributionManagement><relocation><groupId>new</groupId></relocation></distributionManagement>
		</project>`))
	})
	mux.HandleFunc("/new/a/1/a-1.pom", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<project><groupId>new</groupId><artifactId>a</artifactId><version>1</version>
			<name>relocated</name>
		</project>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	var out bytes.Buffer
	if err := run([]string{"-repo", srv.URL, "old:a:1"}, nil, &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	o := decode(t, out.Bytes())
	if o.Relocation == nil || o.Relocation.GAV != "new:a:1" {
		t.Errorf("relocation surfaced: %+v", o.Relocation)
	}

	out.Reset()
	if err := run([]string{"-repo", srv.URL, "-relocate", "old:a:1"}, nil, &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	o = decode(t, out.Bytes())
	if o.GAV != "new:a:1" || o.Name != "relocated" {
		t.Errorf("followed relocation: %+v", o)
	}
}

func TestRunErrors(t *testing.T) {
	if err := run(nil, nil, &bytes.Buffer{}); err == nil {
		t.Error("expected error with no args")
	}
	if err := run([]string{"bad"}, nil, &bytes.Buffer{}); err == nil {
		t.Error("expected error for bad coord")
	}
	if err := run([]string{"-f", "/nonexistent"}, nil, &bytes.Buffer{}); err == nil {
		t.Error("expected error for missing file")
	}
}

func TestParseProfiles(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{profileDefault, profileDefault},
		{"", profileDefault},
		{profilePessimistic, profilePessimistic},
		{"all", profilePessimistic},
		{"a,b", "explicit"},
	}
	for _, tt := range tests {
		got := parseProfiles(tt.in)
		var name string
		switch got.Mode {
		case 0:
			name = profileDefault
		case 1:
			name = profilePessimistic
		default:
			name = "explicit"
		}
		if name != tt.want {
			t.Errorf("parseProfiles(%q): got %v", tt.in, got)
		}
	}
}
