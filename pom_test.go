package pom

import (
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParsePOM(t *testing.T) {
	src := []byte(`<?xml version="1.0" encoding="ISO-8859-1"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <parent>
    <groupId>org.example</groupId>
    <artifactId>parent</artifactId>
    <version>1.0</version>
  </parent>
  <artifactId>child</artifactId>
  <packaging>jar</packaging>
  <properties>
    <foo.version>  1.2.3  </foo.version>
    <empty/>
  </properties>
  <dependencyManagement>
    <dependencies>
      <dependency>
        <groupId>org.bom</groupId>
        <artifactId>bom</artifactId>
        <version>1.0</version>
        <type>pom</type>
        <scope>import</scope>
      </dependency>
    </dependencies>
  </dependencyManagement>
  <dependencies>
    <dependency>
      <groupId>org.foo</groupId>
      <artifactId>foo</artifactId>
      <version>${foo.version}</version>
      <exclusions>
        <exclusion>
          <groupId>org.bad</groupId>
          <artifactId>bad</artifactId>
        </exclusion>
      </exclusions>
    </dependency>
  </dependencies>
  <profiles>
    <profile>
      <id>extra</id>
      <activation><activeByDefault>true</activeByDefault></activation>
      <properties><bar.version>2.0</bar.version></properties>
    </profile>
  </profiles>
</project>`)

	p, err := ParsePOM(src)
	if err != nil {
		t.Fatalf("ParsePOM: %v", err)
	}
	if p.Parent == nil || p.Parent.GroupID != "org.example" {
		t.Fatalf("parent not parsed: %+v", p.Parent)
	}
	if p.Properties["foo.version"] != "1.2.3" {
		t.Errorf("property trimming: got %q", p.Properties["foo.version"])
	}
	if _, ok := p.Properties["empty"]; !ok {
		t.Error("empty property element should yield empty string entry")
	}
	if len(p.DependencyManagement.Dependencies) != 1 || p.DependencyManagement.Dependencies[0].Scope != "import" {
		t.Errorf("depMgmt not parsed: %+v", p.DependencyManagement)
	}
	if len(p.Dependencies) != 1 || len(p.Dependencies[0].Exclusions) != 1 {
		t.Errorf("dependencies/exclusions not parsed: %+v", p.Dependencies)
	}
	if len(p.Profiles) != 1 || p.Profiles[0].ID != "extra" {
		t.Errorf("profiles not parsed: %+v", p.Profiles)
	}
	if p.Profiles[0].Properties["bar.version"] != "2.0" {
		t.Errorf("profile properties not parsed")
	}

	g := p.EffectiveGAV()
	if g.GroupID != "org.example" || g.ArtifactID != "child" || g.Version != "1.0" {
		t.Errorf("EffectiveGAV inherited wrong: %v", g)
	}
}

func TestParseGAV(t *testing.T) {
	tests := []struct {
		in      string
		want    GAV
		wantErr bool
	}{
		{"g:a:v", GAV{"g", "a", "v"}, false},
		{"g:a", GAV{"g", "a", ""}, false},
		{"g:a:v:jar", GAV{"g", "a", "v"}, false},
		{"bad", GAV{}, true},
	}
	for _, tt := range tests {
		got, err := ParseGAV(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseGAV(%q) err=%v wantErr=%v", tt.in, err, tt.wantErr)
		}
		if got != tt.want {
			t.Errorf("ParseGAV(%q) = %v want %v", tt.in, got, tt.want)
		}
	}
}

func TestParsePOMError(t *testing.T) {
	if _, err := ParsePOM([]byte("not xml at all <<<")); err == nil {
		t.Error("expected parse error")
	}
}

func TestParsePOMMatchesXMLDecoder(t *testing.T) {
	files, err := filepath.Glob("testdata/poms/*.pom")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		want, err := decodePOMReflect(data)
		if err != nil {
			t.Fatalf("reference parse %s: %v", path, err)
		}
		got, err := ParsePOM(data)
		if err != nil {
			t.Fatalf("ParsePOM %s: %v", path, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("ParsePOM %s differs from encoding/xml", path)
		}
	}
}

func TestParsePOMMatchesXMLDecoderWithUTF8BOM(t *testing.T) {
	data := []byte("\xEF\xBB\xBF<?xml version=\"1.0\"?><project><groupId>org.example</groupId></project>")
	want, err := decodePOMReflect(data)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParsePOM(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParsePOM with UTF-8 BOM = %+v, want %+v", got, want)
	}
}

func TestParsePOMMatchesXMLDecoderErrors(t *testing.T) {
	inputs := []string{
		"",
		"plain text",
		"<not-project/>",
		"<project><groupId>",
		"<project><dependencies><dependency></project>",
		"<project>&unknown;</project>",
		"<project/><project/>",
	}
	for _, input := range inputs {
		_, wantErr := decodePOMReflect([]byte(input))
		_, gotErr := ParsePOM([]byte(input))
		if (gotErr != nil) != (wantErr != nil) {
			t.Errorf("ParsePOM(%q) error = %v, reference error = %v", input, gotErr, wantErr)
		}
	}
}

func TestParsePOMMatchesXMLDecoderNestedText(t *testing.T) {
	data := []byte(`<project>
		<groupId>before<ignored>nested</ignored>after</groupId>
		<properties><value>left<ignored>nested</ignored>right</value></properties>
	</project>`)
	want, err := decodePOMReflect(data)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParsePOM(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParsePOM nested text = %+v, want %+v", got, want)
	}
}

func decodePOMReflect(data []byte) (*POM, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	dec.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) {
		return input, nil
	}
	var p POM
	if err := dec.Decode(&p); err != nil {
		return nil, err
	}
	return &p, nil
}

func TestManagementKey(t *testing.T) {
	d := Dep{GroupID: "g", ArtifactID: "a"}
	if d.managementKey() != "g:a:jar:" {
		t.Errorf("default key: %q", d.managementKey())
	}
	d.Type = "test-jar"
	d.Classifier = "tests"
	if d.managementKey() != "g:a:test-jar:tests" {
		t.Errorf("custom key: %q", d.managementKey())
	}
}
