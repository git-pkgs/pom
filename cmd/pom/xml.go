package main

import (
	"encoding/xml"
	"io"
	"sort"

	"github.com/git-pkgs/pom"
)

// writeXML emits a minimal effective-pom: just the merged, interpolated
// fields a downstream POM parser would read. It is not a faithful
// reproduction of `mvn help:effective-pom` (no plugins, build, reporting,
// repositories) but it satisfies callers that only need metadata and
// dependencies.
func writeXML(w io.Writer, ep *pom.EffectivePOM) error {
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	defer func() { _ = enc.Close() }()
	return enc.Encode(toXMLProject(ep))
}

type xmlProject struct {
	XMLName     xml.Name `xml:"project"`
	GroupID     string   `xml:"groupId"`
	ArtifactID  string   `xml:"artifactId"`
	Version     string   `xml:"version"`
	Packaging   string   `xml:"packaging"`
	Name        string   `xml:"name,omitempty"`
	Description string   `xml:"description,omitempty"`
	URL         string   `xml:"url,omitempty"`

	Licenses *xmlLicenses `xml:"licenses,omitempty"`
	SCM      *xmlSCM      `xml:"scm,omitempty"`

	DistributionManagement *xmlDistMgmt `xml:"distributionManagement,omitempty"`

	Properties   xmlProps `xml:"properties,omitempty"`
	Dependencies xmlDeps  `xml:"dependencies"`
}

type xmlLicenses struct {
	License []xmlLicense `xml:"license"`
}

type xmlLicense struct {
	Name string `xml:"name,omitempty"`
	URL  string `xml:"url,omitempty"`
}

type xmlSCM struct {
	URL                 string `xml:"url,omitempty"`
	Connection          string `xml:"connection,omitempty"`
	DeveloperConnection string `xml:"developerConnection,omitempty"`
}

type xmlDistMgmt struct {
	Relocation *xmlRelocation `xml:"relocation,omitempty"`
}

type xmlRelocation struct {
	GroupID    string `xml:"groupId,omitempty"`
	ArtifactID string `xml:"artifactId,omitempty"`
	Version    string `xml:"version,omitempty"`
	Message    string `xml:"message,omitempty"`
}

type xmlDeps struct {
	Dependency []xmlDep `xml:"dependency"`
}

type xmlDep struct {
	GroupID    string `xml:"groupId"`
	ArtifactID string `xml:"artifactId"`
	Version    string `xml:"version,omitempty"`
	Type       string `xml:"type,omitempty"`
	Classifier string `xml:"classifier,omitempty"`
	Scope      string `xml:"scope,omitempty"`
	Optional   string `xml:"optional,omitempty"`
}

type xmlProps map[string]string

func (p xmlProps) MarshalXML(e *xml.Encoder, start xml.StartElement) error {
	if len(p) == 0 {
		return nil
	}
	if err := e.EncodeToken(start); err != nil {
		return err
	}
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := e.EncodeElement(p[k], xml.StartElement{Name: xml.Name{Local: k}}); err != nil {
			return err
		}
	}
	return e.EncodeToken(start.End())
}

func toXMLProject(ep *pom.EffectivePOM) xmlProject {
	p := xmlProject{
		GroupID:     ep.GAV.GroupID,
		ArtifactID:  ep.GAV.ArtifactID,
		Version:     ep.GAV.Version,
		Packaging:   ep.Packaging,
		Name:        ep.Name,
		Description: ep.Description,
		URL:         ep.URL,
		Properties:  xmlProps(ep.Properties),
	}
	if len(ep.Licenses) > 0 {
		p.Licenses = &xmlLicenses{}
		for _, l := range ep.Licenses {
			p.Licenses.License = append(p.Licenses.License, xmlLicense{Name: l.Name, URL: l.URL})
		}
	}
	if ep.SCM.URL != "" || ep.SCM.Connection != "" || ep.SCM.DeveloperConnection != "" {
		p.SCM = &xmlSCM{URL: ep.SCM.URL, Connection: ep.SCM.Connection, DeveloperConnection: ep.SCM.DeveloperConnection}
	}
	if ep.Relocation != nil {
		p.DistributionManagement = &xmlDistMgmt{Relocation: &xmlRelocation{
			GroupID:    ep.Relocation.GroupID,
			ArtifactID: ep.Relocation.ArtifactID,
			Version:    ep.Relocation.Version,
			Message:    ep.Relocation.Message,
		}}
	}
	for _, d := range ep.Dependencies {
		xd := xmlDep{
			GroupID:    d.GroupID,
			ArtifactID: d.ArtifactID,
			Version:    d.Version,
			Scope:      d.Scope,
		}
		if d.Type != "jar" {
			xd.Type = d.Type
		}
		if d.Classifier != "" {
			xd.Classifier = d.Classifier
		}
		if d.Optional {
			xd.Optional = "true"
		}
		p.Dependencies.Dependency = append(p.Dependencies.Dependency, xd)
	}
	return p
}
