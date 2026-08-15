package pom

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"
)

const (
	elementArtifactID   = "artifactId"
	elementDependencies = "dependencies"
	elementGroupID      = "groupId"
	elementName         = "name"
	elementProject      = "project"
	elementURL          = "url"
	elementVersion      = "version"
)

func decodePOM(dec *xml.Decoder) (*POM, error) {
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch tok := tok.(type) {
		case xml.StartElement:
			if tok.Name.Local != elementProject {
				return nil, fmt.Errorf("expected element type <project> but have <%s>", tok.Name.Local)
			}
			p := &POM{XMLName: tok.Name}
			if err := decodeProject(dec, tok, p); err != nil {
				return nil, err
			}
			return p, nil
		case xml.CharData:
			if len(bytes.TrimSpace(tok)) != 0 {
				return nil, fmt.Errorf("expected element type <project> but have character data")
			}
		}
	}
}

func decodeProject(dec *xml.Decoder, start xml.StartElement, p *POM) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch tok := tok.(type) {
		case xml.StartElement:
			switch tok.Name.Local {
			case elementGroupID:
				p.GroupID, err = decodeText(dec, tok)
			case elementArtifactID:
				p.ArtifactID, err = decodeText(dec, tok)
			case elementVersion:
				p.Version, err = decodeText(dec, tok)
			case "packaging":
				p.Packaging, err = decodeText(dec, tok)
			case "parent":
				p.Parent, err = decodeParent(dec, tok)
			case elementName:
				p.Name, err = decodeText(dec, tok)
			case "description":
				p.Description, err = decodeText(dec, tok)
			case elementURL:
				p.URL, err = decodeText(dec, tok)
			case "licenses":
				err = decodeLicenses(dec, tok, &p.Licenses)
			case "scm":
				err = decodeSCM(dec, tok, &p.SCM)
			case "distributionManagement":
				err = decodeDistMgmt(dec, tok, &p.DistributionManagement)
			case "properties":
				p.Properties, err = decodeProperties(dec, tok)
			case elementDependencies:
				err = decodeDependencies(dec, tok, &p.Dependencies)
			case "dependencyManagement":
				err = decodeDepMgmt(dec, tok, &p.DependencyManagement)
			case "build":
				err = decodeBuild(dec, tok, &p.Build)
			case "profiles":
				err = decodeProfiles(dec, tok, &p.Profiles)
			default:
				err = dec.Skip()
			}
			if err != nil {
				return err
			}
		case xml.EndElement:
			if tok.Name == start.Name {
				return nil
			}
		}
	}
}

func decodeParent(dec *xml.Decoder, start xml.StartElement) (*Parent, error) {
	p := &Parent{}
	err := decodeFields(dec, start, func(child xml.StartElement) error {
		var err error
		switch child.Name.Local {
		case elementGroupID:
			p.GroupID, err = decodeText(dec, child)
		case elementArtifactID:
			p.ArtifactID, err = decodeText(dec, child)
		case elementVersion:
			p.Version, err = decodeText(dec, child)
		case "relativePath":
			var path string
			path, err = decodeText(dec, child)
			p.RelativePath = &path
		default:
			err = dec.Skip()
		}
		return err
	})
	return p, err
}

func decodeLicenses(dec *xml.Decoder, start xml.StartElement, licenses *[]License) error {
	return decodeFields(dec, start, func(child xml.StartElement) error {
		if child.Name.Local != "license" {
			return dec.Skip()
		}
		var license License
		if err := decodeFields(dec, child, func(field xml.StartElement) error {
			var err error
			switch field.Name.Local {
			case elementName:
				license.Name, err = decodeText(dec, field)
			case elementURL:
				license.URL, err = decodeText(dec, field)
			default:
				err = dec.Skip()
			}
			return err
		}); err != nil {
			return err
		}
		*licenses = append(*licenses, license)
		return nil
	})
}

func decodeSCM(dec *xml.Decoder, start xml.StartElement, scm *SCM) error {
	return decodeFields(dec, start, func(child xml.StartElement) error {
		var err error
		switch child.Name.Local {
		case elementURL:
			scm.URL, err = decodeText(dec, child)
		case "connection":
			scm.Connection, err = decodeText(dec, child)
		case "developerConnection":
			scm.DeveloperConnection, err = decodeText(dec, child)
		default:
			err = dec.Skip()
		}
		return err
	})
}

func decodeDistMgmt(dec *xml.Decoder, start xml.StartElement, dist *DistMgmt) error {
	return decodeFields(dec, start, func(child xml.StartElement) error {
		if child.Name.Local != "relocation" {
			return dec.Skip()
		}
		relocation := &Relocation{}
		if err := decodeFields(dec, child, func(field xml.StartElement) error {
			var err error
			switch field.Name.Local {
			case elementGroupID:
				relocation.GroupID, err = decodeText(dec, field)
			case elementArtifactID:
				relocation.ArtifactID, err = decodeText(dec, field)
			case elementVersion:
				relocation.Version, err = decodeText(dec, field)
			case "message":
				relocation.Message, err = decodeText(dec, field)
			default:
				err = dec.Skip()
			}
			return err
		}); err != nil {
			return err
		}
		dist.Relocation = relocation
		return nil
	})
}

func decodeProperties(dec *xml.Decoder, start xml.StartElement) (Properties, error) {
	properties := Properties{}
	err := decodeFields(dec, start, func(child xml.StartElement) error {
		value, err := decodeText(dec, child)
		if err == nil {
			properties[child.Name.Local] = strings.TrimSpace(value)
		}
		return err
	})
	return properties, err
}

func decodeDependencies(dec *xml.Decoder, start xml.StartElement, deps *[]Dep) error {
	return decodeFields(dec, start, func(child xml.StartElement) error {
		if child.Name.Local != "dependency" {
			return dec.Skip()
		}
		dep, err := decodeDep(dec, child)
		if err == nil {
			*deps = append(*deps, dep)
		}
		return err
	})
}

func decodeDepMgmt(dec *xml.Decoder, start xml.StartElement, depMgmt *DepMgmt) error {
	return decodeFields(dec, start, func(child xml.StartElement) error {
		if child.Name.Local != elementDependencies {
			return dec.Skip()
		}
		return decodeDependencies(dec, child, &depMgmt.Dependencies)
	})
}

func decodeDep(dec *xml.Decoder, start xml.StartElement) (Dep, error) {
	var dep Dep
	err := decodeFields(dec, start, func(child xml.StartElement) error {
		var err error
		switch child.Name.Local {
		case elementGroupID:
			dep.GroupID, err = decodeText(dec, child)
		case elementArtifactID:
			dep.ArtifactID, err = decodeText(dec, child)
		case elementVersion:
			dep.Version, err = decodeText(dec, child)
		case "type":
			dep.Type, err = decodeText(dec, child)
		case "classifier":
			dep.Classifier, err = decodeText(dec, child)
		case "scope":
			dep.Scope, err = decodeText(dec, child)
		case "optional":
			dep.Optional, err = decodeText(dec, child)
		case "exclusions":
			err = decodeExclusions(dec, child, &dep.Exclusions)
		default:
			err = dec.Skip()
		}
		return err
	})
	return dep, err
}

func decodeBuild(dec *xml.Decoder, start xml.StartElement, build *Build) error {
	return decodeFields(dec, start, func(child xml.StartElement) error {
		switch child.Name.Local {
		case "plugins":
			return decodePlugins(dec, child, &build.Plugins)
		case "pluginManagement":
			return decodePluginManagement(dec, child, &build.PluginManagement)
		case "extensions":
			return decodeExtensions(dec, child, &build.Extensions)
		default:
			return dec.Skip()
		}
	})
}

func decodePlugins(dec *xml.Decoder, start xml.StartElement, plugins *[]Plugin) error {
	return decodeFields(dec, start, func(child xml.StartElement) error {
		if child.Name.Local != "plugin" {
			return dec.Skip()
		}
		plugin, err := decodePlugin(dec, child)
		if err == nil {
			*plugins = append(*plugins, plugin)
		}
		return err
	})
}

func decodePluginManagement(dec *xml.Decoder, start xml.StartElement, management *PluginManagement) error {
	return decodeFields(dec, start, func(child xml.StartElement) error {
		if child.Name.Local != "plugins" {
			return dec.Skip()
		}
		return decodePlugins(dec, child, &management.Plugins)
	})
}

func decodePlugin(dec *xml.Decoder, start xml.StartElement) (Plugin, error) {
	var plugin Plugin
	err := decodeFields(dec, start, func(child xml.StartElement) error {
		var err error
		switch child.Name.Local {
		case elementGroupID:
			plugin.GroupID, err = decodeText(dec, child)
		case elementArtifactID:
			plugin.ArtifactID, err = decodeText(dec, child)
		case elementVersion:
			plugin.Version, err = decodeText(dec, child)
		case elementDependencies:
			err = decodeDependencies(dec, child, &plugin.Dependencies)
		default:
			err = dec.Skip()
		}
		return err
	})
	return plugin, err
}

func decodeExtensions(dec *xml.Decoder, start xml.StartElement, extensions *[]Extension) error {
	return decodeFields(dec, start, func(child xml.StartElement) error {
		if child.Name.Local != "extension" {
			return dec.Skip()
		}
		extension, err := decodeExtension(dec, child)
		if err == nil {
			*extensions = append(*extensions, extension)
		}
		return err
	})
}

func decodeExtension(dec *xml.Decoder, start xml.StartElement) (Extension, error) {
	var extension Extension
	err := decodeFields(dec, start, func(child xml.StartElement) error {
		var err error
		switch child.Name.Local {
		case elementGroupID:
			extension.GroupID, err = decodeText(dec, child)
		case elementArtifactID:
			extension.ArtifactID, err = decodeText(dec, child)
		case elementVersion:
			extension.Version, err = decodeText(dec, child)
		default:
			err = dec.Skip()
		}
		return err
	})
	return extension, err
}

func decodeExclusions(dec *xml.Decoder, start xml.StartElement, exclusions *[]Exclusion) error {
	return decodeFields(dec, start, func(child xml.StartElement) error {
		if child.Name.Local != "exclusion" {
			return dec.Skip()
		}
		var exclusion Exclusion
		if err := decodeFields(dec, child, func(field xml.StartElement) error {
			var err error
			switch field.Name.Local {
			case elementGroupID:
				exclusion.GroupID, err = decodeText(dec, field)
			case elementArtifactID:
				exclusion.ArtifactID, err = decodeText(dec, field)
			default:
				err = dec.Skip()
			}
			return err
		}); err != nil {
			return err
		}
		*exclusions = append(*exclusions, exclusion)
		return nil
	})
}

func decodeProfiles(dec *xml.Decoder, start xml.StartElement, profiles *[]Profile) error {
	return decodeFields(dec, start, func(child xml.StartElement) error {
		if child.Name.Local != "profile" {
			return dec.Skip()
		}
		profile, err := decodeProfile(dec, child)
		if err == nil {
			*profiles = append(*profiles, profile)
		}
		return err
	})
}

func decodeProfile(dec *xml.Decoder, start xml.StartElement) (Profile, error) {
	var profile Profile
	err := decodeFields(dec, start, func(child xml.StartElement) error {
		var err error
		switch child.Name.Local {
		case "id":
			profile.ID, err = decodeText(dec, child)
		case "activation":
			err = decodeActivation(dec, child, &profile.Activation)
		case "properties":
			profile.Properties, err = decodeProperties(dec, child)
		case elementDependencies:
			err = decodeDependencies(dec, child, &profile.Dependencies)
		case "dependencyManagement":
			err = decodeDepMgmt(dec, child, &profile.DependencyManagement)
		case "build":
			err = decodeBuild(dec, child, &profile.Build)
		default:
			err = dec.Skip()
		}
		return err
	})
	return profile, err
}

func decodeActivation(dec *xml.Decoder, start xml.StartElement, activation *Activation) error {
	return decodeFields(dec, start, func(child xml.StartElement) error {
		var err error
		switch child.Name.Local {
		case "activeByDefault":
			activation.ActiveByDefault, err = decodeText(dec, child)
		case "jdk":
			activation.JDK, err = decodeText(dec, child)
		case "os":
			err = decodeFields(dec, child, func(field xml.StartElement) error {
				var fieldErr error
				switch field.Name.Local {
				case elementName:
					activation.OS.Name, fieldErr = decodeText(dec, field)
				case "family":
					activation.OS.Family, fieldErr = decodeText(dec, field)
				case "arch":
					activation.OS.Arch, fieldErr = decodeText(dec, field)
				default:
					fieldErr = dec.Skip()
				}
				return fieldErr
			})
		case "property":
			err = decodeFields(dec, child, func(field xml.StartElement) error {
				var fieldErr error
				switch field.Name.Local {
				case elementName:
					activation.Property.Name, fieldErr = decodeText(dec, field)
				case "value":
					activation.Property.Value, fieldErr = decodeText(dec, field)
				default:
					fieldErr = dec.Skip()
				}
				return fieldErr
			})
		default:
			err = dec.Skip()
		}
		return err
	})
}

func decodeFields(dec *xml.Decoder, start xml.StartElement, decode func(xml.StartElement) error) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch tok := tok.(type) {
		case xml.StartElement:
			if err := decode(tok); err != nil {
				return err
			}
		case xml.EndElement:
			if tok.Name == start.Name {
				return nil
			}
		}
	}
}

func decodeText(dec *xml.Decoder, start xml.StartElement) (string, error) {
	var first string
	var text strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", err
		}
		switch tok := tok.(type) {
		case xml.CharData:
			part := string(tok)
			if first == "" && text.Len() == 0 {
				first = part
				continue
			}
			if text.Len() == 0 {
				text.Grow(len(first) + len(tok))
				text.WriteString(first)
				first = ""
			}
			text.WriteString(part)
		case xml.StartElement:
			if err := dec.Skip(); err != nil {
				return "", err
			}
		case xml.EndElement:
			if tok.Name == start.Name {
				if text.Len() != 0 {
					return text.String(), nil
				}
				return first, nil
			}
		}
	}
}
