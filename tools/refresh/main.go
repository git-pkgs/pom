// Command refresh downloads a corpus of real POMs (including their full
// parent and BOM closure) into testdata/poms, then runs the real
// `mvn help:effective-pom` against each root and writes the resulting
// dependency list to testdata/expected as JSON. The golden test in the
// parent package compares the pure-Go resolver against these files.
//
// Run with: go run ./tools/refresh
//
// Requires network access and a working `mvn` on PATH.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/git-pkgs/pom"
)

var corpus = []string{
	"com.fasterxml.jackson.core:jackson-databind:2.17.2",
	"org.springframework.boot:spring-boot-starter-web:3.3.4",
	"org.junit.jupiter:junit-jupiter:5.10.3",
	"com.google.guava:guava:33.2.1-jre",
	"org.apache.logging.log4j:log4j-core:2.23.1",
	"org.apache.commons:commons-lang3:3.14.0",
	"io.netty:netty-handler:4.1.112.Final",
	"com.squareup.okhttp3:okhttp:4.12.0",
	"org.slf4j:slf4j-api:2.0.13",

	"org.springframework:spring-context:6.1.13",
	"org.jetbrains.kotlin:kotlin-stdlib:1.9.24",
	"org.apache.httpcomponents.client5:httpclient5:5.3.1",
	"io.micrometer:micrometer-core:1.13.4",
	"com.google.code.gson:gson:2.11.0",
	"com.google.protobuf:protobuf-java:3.25.3",
	"io.grpc:grpc-core:1.66.0",
	"org.mockito:mockito-core:5.12.0",
	"org.assertj:assertj-core:3.26.3",
	"org.yaml:snakeyaml:2.2",
	"org.postgresql:postgresql:42.7.3",
	"com.github.ben-manes.caffeine:caffeine:3.1.8",
	"io.projectreactor:reactor-core:3.6.10",
	"org.testcontainers:testcontainers:1.20.1",
	"org.projectlombok:lombok:1.18.34",
	"org.apache.kafka:kafka-clients:3.8.0",
	"io.opentelemetry:opentelemetry-api:1.42.1",
	"ch.qos.logback:logback-classic:1.5.8",
	"org.hibernate.orm:hibernate-core:6.5.2.Final",
	"jakarta.servlet:jakarta.servlet-api:6.0.0",
	"com.zaxxer:HikariCP:5.1.0",

	// Repro cases from scrutineer#46.
	"org.reflections:reflections:0.10.2",
	"org.modelmapper:modelmapper:3.2.0",
}

const (
	pomsDir     = "testdata/poms"
	expectedDir = "testdata/expected"
	dirMode     = 0o755
	fileMode    = 0o644
	timeout     = 10 * time.Minute
)

type expectedDep struct {
	GroupID    string `json:"groupId"`
	ArtifactID string `json:"artifactId"`
	Version    string `json:"version"`
	Scope      string `json:"scope"`
	Type       string `json:"type"`
	Classifier string `json:"classifier,omitempty"`
	Optional   bool   `json:"optional"`
}

type expectedFile struct {
	GAV          string        `json:"gav"`
	Dependencies []expectedDep `json:"dependencies"`
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	for _, dir := range []string{pomsDir, expectedDir} {
		if err := os.MkdirAll(dir, dirMode); err != nil {
			return err
		}
	}

	rec := &recordingFetcher{inner: pom.NewHTTPFetcher(""), seen: map[pom.GAV]bool{}}
	resolver := pom.NewResolver(rec)

	for _, coord := range corpus {
		gav, err := pom.ParseGAV(coord)
		if err != nil {
			return fmt.Errorf("bad coord %q: %w", coord, err)
		}
		log.Printf("== %s", gav)
		ep, err := resolver.Resolve(ctx, gav, pom.Options{})
		if err != nil {
			return fmt.Errorf("resolve %s: %w", gav, err)
		}
		for _, w := range ep.Warnings {
			log.Printf("  warn: %s", w)
		}
		if err := writeExpected(ctx, gav); err != nil {
			return fmt.Errorf("effective-pom for %s: %w", gav, err)
		}
	}
	log.Printf("done: %d poms in %s", len(rec.seen), pomsDir)
	return nil
}

// recordingFetcher wraps HTTPFetcher and writes every fetched POM to
// testdata/poms so the test suite can run offline against the same bytes
// the real Maven saw.
type recordingFetcher struct {
	inner *pom.HTTPFetcher
	seen  map[pom.GAV]bool
}

func (f *recordingFetcher) Fetch(ctx context.Context, gav pom.GAV) (*pom.POM, error) {
	data, err := f.inner.FetchBytes(ctx, gav)
	if err != nil {
		return nil, err
	}
	if !f.seen[gav] {
		if err := os.WriteFile(filepath.Join(pomsDir, pom.FixtureName(gav)), data, fileMode); err != nil {
			return nil, err
		}
		f.seen[gav] = true
	}
	return pom.ParsePOM(data)
}

// writeExpected runs `mvn help:effective-pom` on gav in a temp dir and
// writes the resulting <dependencies> as JSON.
func writeExpected(ctx context.Context, gav pom.GAV) error {
	tmp, err := os.MkdirTemp("", "pom-refresh-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	pomPath := filepath.Join(tmp, "pom.xml")
	src, err := os.ReadFile(filepath.Join(pomsDir, pom.FixtureName(gav)))
	if err != nil {
		return err
	}
	if err := os.WriteFile(pomPath, src, fileMode); err != nil {
		return err
	}

	outPath := filepath.Join(tmp, "effective.xml")
	cmd := exec.CommandContext(ctx, "mvn", "-q", "-B",
		"-f", pomPath,
		"help:effective-pom",
		"-Doutput="+outPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mvn help:effective-pom: %w\n%s", err, stderr.String())
	}

	eff, err := os.ReadFile(outPath)
	if err != nil {
		return err
	}
	deps, err := extractDeps(eff)
	if err != nil {
		return fmt.Errorf("parse effective pom: %w", err)
	}

	exp := expectedFile{GAV: gav.String(), Dependencies: deps}
	data, err := json.MarshalIndent(exp, "", "  ")
	if err != nil {
		return err
	}
	name := fmt.Sprintf("%s_%s_%s.json", gav.GroupID, gav.ArtifactID, gav.Version)
	return os.WriteFile(filepath.Join(expectedDir, name), append(data, '\n'), fileMode)
}

type effProject struct {
	Dependencies struct {
		Dependency []effDep `xml:"dependency"`
	} `xml:"dependencies"`
}

type effDep struct {
	GroupID    string `xml:"groupId"`
	ArtifactID string `xml:"artifactId"`
	Version    string `xml:"version"`
	Scope      string `xml:"scope"`
	Type       string `xml:"type"`
	Classifier string `xml:"classifier"`
	Optional   string `xml:"optional"`
}

func extractDeps(data []byte) ([]expectedDep, error) {
	var p effProject
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	if err := dec.Decode(&p); err != nil {
		return nil, err
	}
	out := make([]expectedDep, 0, len(p.Dependencies.Dependency))
	for _, d := range p.Dependencies.Dependency {
		t := d.Type
		if t == "" {
			t = "jar"
		}
		s := d.Scope
		if s == "" {
			s = "compile"
		}
		out = append(out, expectedDep{
			GroupID:    d.GroupID,
			ArtifactID: d.ArtifactID,
			Version:    d.Version,
			Scope:      s,
			Type:       t,
			Classifier: d.Classifier,
			Optional:   strings.EqualFold(strings.TrimSpace(d.Optional), "true"),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].GroupID != out[j].GroupID {
			return out[i].GroupID < out[j].GroupID
		}
		if out[i].ArtifactID != out[j].ArtifactID {
			return out[i].ArtifactID < out[j].ArtifactID
		}
		return out[i].Classifier < out[j].Classifier
	})
	return out, nil
}
