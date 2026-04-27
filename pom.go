// Package pom computes a useful subset of Maven's effective POM in pure Go.
//
// It resolves parent chains, merges properties and dependencyManagement,
// imports BOMs, applies profiles, and interpolates ${...} expressions so
// that callers receive concrete dependency requirements suitable for
// vulnerability matching and supply-chain analysis. It does not attempt
// plugin merging, lifecycle binding, or any build-time semantics.
package pom

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// GAV identifies a Maven artifact by group, artifact and version.
type GAV struct {
	GroupID    string
	ArtifactID string
	Version    string
}

func (g GAV) String() string {
	return g.GroupID + ":" + g.ArtifactID + ":" + g.Version
}

// GA returns the group:artifact key without version, used for
// dependencyManagement lookups.
func (g GAV) GA() string {
	return g.GroupID + ":" + g.ArtifactID
}

const (
	minCoordParts       = 2
	versionedCoordParts = 3
	maxCoordParts       = 4
	scopeImport         = "import"
)

// ParseGAV parses "g:a:v" or "g:a" into a GAV.
func ParseGAV(s string) (GAV, error) {
	parts := strings.SplitN(s, ":", maxCoordParts)
	if len(parts) < minCoordParts {
		return GAV{}, fmt.Errorf("pom: invalid coordinate %q", s)
	}
	g := GAV{GroupID: parts[0], ArtifactID: parts[1]}
	if len(parts) >= versionedCoordParts {
		g.Version = parts[2]
	}
	return g, nil
}

// POM is the parsed subset of a project object model that the resolver
// cares about. Fields are raw (uninterpolated) as read from XML.
type POM struct {
	XMLName xml.Name `xml:"project"`

	GroupID    string `xml:"groupId"`
	ArtifactID string `xml:"artifactId"`
	Version    string `xml:"version"`
	Packaging  string `xml:"packaging"`

	Parent *Parent `xml:"parent"`

	Name        string    `xml:"name"`
	Description string    `xml:"description"`
	URL         string    `xml:"url"`
	Licenses    []License `xml:"licenses>license"`
	SCM         SCM       `xml:"scm"`

	DistributionManagement DistMgmt `xml:"distributionManagement"`

	Properties           Properties `xml:"properties"`
	Dependencies         []Dep      `xml:"dependencies>dependency"`
	DependencyManagement DepMgmt    `xml:"dependencyManagement"`

	Profiles []Profile `xml:"profiles>profile"`
}

// Parent is the <parent> coordinate reference.
type Parent struct {
	GroupID      string  `xml:"groupId"`
	ArtifactID   string  `xml:"artifactId"`
	Version      string  `xml:"version"`
	RelativePath *string `xml:"relativePath"`
}

// DefaultRelativePath is what Maven assumes when <relativePath> is omitted.
const DefaultRelativePath = "../pom.xml"

// LocalPath returns the relative path to the parent POM, applying Maven's
// default of "../pom.xml" when the element is absent. An explicit empty
// element (<relativePath/>) means "skip local lookup" and returns "".
func (p *Parent) LocalPath() string {
	if p.RelativePath == nil {
		return DefaultRelativePath
	}
	return strings.TrimSpace(*p.RelativePath)
}

func (p *Parent) GAV() GAV {
	return GAV{GroupID: p.GroupID, ArtifactID: p.ArtifactID, Version: p.Version}
}

// License is a <license> entry.
type License struct {
	Name string `xml:"name"`
	URL  string `xml:"url"`
}

// SCM is the <scm> block.
type SCM struct {
	URL                 string `xml:"url"`
	Connection          string `xml:"connection"`
	DeveloperConnection string `xml:"developerConnection"`
}

func (s SCM) empty() bool {
	return s.URL == "" && s.Connection == "" && s.DeveloperConnection == ""
}

// DistMgmt is the subset of <distributionManagement> we care about.
type DistMgmt struct {
	Relocation *Relocation `xml:"relocation"`
}

// Relocation is a <relocation> redirect to another coordinate.
type Relocation struct {
	GroupID    string `xml:"groupId"`
	ArtifactID string `xml:"artifactId"`
	Version    string `xml:"version"`
	Message    string `xml:"message"`
}

// Target returns the coordinate this relocation points to, filling any
// omitted parts from the relocating POM's own coordinates.
func (r *Relocation) Target(from GAV) GAV {
	to := from
	if r.GroupID != "" {
		to.GroupID = r.GroupID
	}
	if r.ArtifactID != "" {
		to.ArtifactID = r.ArtifactID
	}
	if r.Version != "" {
		to.Version = r.Version
	}
	return to
}

// DepMgmt wraps the <dependencyManagement> section.
type DepMgmt struct {
	Dependencies []Dep `xml:"dependencies>dependency"`
}

// Dep is a <dependency> entry. Values are raw and may contain ${...}
// expressions until interpolation runs.
type Dep struct {
	GroupID    string      `xml:"groupId"`
	ArtifactID string      `xml:"artifactId"`
	Version    string      `xml:"version"`
	Type       string      `xml:"type"`
	Classifier string      `xml:"classifier"`
	Scope      string      `xml:"scope"`
	Optional   string      `xml:"optional"`
	Exclusions []Exclusion `xml:"exclusions>exclusion"`
}

// Exclusion is a <exclusion> entry under a dependency.
type Exclusion struct {
	GroupID    string `xml:"groupId"`
	ArtifactID string `xml:"artifactId"`
}

func (d Dep) GAV() GAV {
	return GAV{GroupID: d.GroupID, ArtifactID: d.ArtifactID, Version: d.Version}
}

// managementKey is the identity Maven uses to match a <dependency> against
// a <dependencyManagement> entry: groupId:artifactId:type:classifier.
func (d Dep) managementKey() string {
	t := d.Type
	if t == "" {
		t = "jar"
	}
	return d.GroupID + ":" + d.ArtifactID + ":" + t + ":" + d.Classifier
}

// Profile is the subset of <profile> needed for dependency resolution.
type Profile struct {
	ID                   string     `xml:"id"`
	Activation           Activation `xml:"activation"`
	Properties           Properties `xml:"properties"`
	Dependencies         []Dep      `xml:"dependencies>dependency"`
	DependencyManagement DepMgmt    `xml:"dependencyManagement"`
}

// Activation holds the parts of <activation> relevant to static evaluation.
type Activation struct {
	ActiveByDefault string `xml:"activeByDefault"`
	JDK             string `xml:"jdk"`
	OS              struct {
		Name   string `xml:"name"`
		Family string `xml:"family"`
		Arch   string `xml:"arch"`
	} `xml:"os"`
	Property struct {
		Name  string `xml:"name"`
		Value string `xml:"value"`
	} `xml:"property"`
}

// Properties holds <properties> children as a key/value map. The XML
// element names are arbitrary so a custom unmarshaler is required.
type Properties map[string]string

func (p *Properties) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	m := Properties{}
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			var v string
			if err := d.DecodeElement(&v, &t); err != nil {
				return err
			}
			m[t.Name.Local] = strings.TrimSpace(v)
		case xml.EndElement:
			if t.Name.Local == start.Name.Local {
				*p = m
				return nil
			}
		}
	}
	*p = m
	return nil
}

// ParsePOM decodes a POM from XML bytes. It is lenient about charset
// declarations and strict-mode failures that would otherwise reject
// real-world POMs published to Maven Central.
func ParsePOM(data []byte) (*POM, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	dec.CharsetReader = func(charset string, input io.Reader) (io.Reader, error) {
		return input, nil
	}
	var p POM
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("pom: parse pom: %w", err)
	}
	return &p, nil
}

// EffectiveGAV returns the POM's own coordinates, falling back to the
// parent's groupId/version when the child omits them.
func (p *POM) EffectiveGAV() GAV {
	g := GAV{GroupID: p.GroupID, ArtifactID: p.ArtifactID, Version: p.Version}
	if p.Parent != nil {
		if g.GroupID == "" {
			g.GroupID = p.Parent.GroupID
		}
		if g.Version == "" {
			g.Version = p.Parent.Version
		}
	}
	return g
}
