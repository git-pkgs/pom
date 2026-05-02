// Command pom resolves a Maven artifact's effective POM in pure Go and
// prints the result as JSON. It is a drop-in replacement for shelling out
// to `mvn help:effective-pom` when all you need is interpolated
// dependencies and merged metadata.
//
// Usage:
//
//	pom [flags] <groupId:artifactId:version>
//	pom [flags] -f pom.xml
//	cat pom.xml | pom [flags] -f -
//
// Flags:
//
//	-repo URL     Maven repository base URL (default: Maven Central)
//	-f path       read root POM from path ("-" for stdin) instead of fetching
//	-profiles m   profile mode: default | pessimistic | id1,id2,...
//	-relocate     follow <relocation> and resolve the target instead
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/git-pkgs/pom"
)

const (
	defaultTimeout = 60 * time.Second
	relocateLimit  = 5
)

type output struct {
	GAV          string      `json:"gav"`
	Packaging    string      `json:"packaging"`
	Name         string      `json:"name,omitempty"`
	Description  string      `json:"description,omitempty"`
	URL          string      `json:"url,omitempty"`
	Licenses     []license   `json:"licenses,omitempty"`
	SCM          *scm        `json:"scm,omitempty"`
	Relocation   *relocation `json:"relocation,omitempty"`
	Parents      []string    `json:"parents,omitempty"`
	Dependencies []dep       `json:"dependencies"`
	Warnings     []string    `json:"warnings,omitempty"`
}

type license struct {
	Name string `json:"name,omitempty"`
	URL  string `json:"url,omitempty"`
}

type scm struct {
	URL                 string `json:"url,omitempty"`
	Connection          string `json:"connection,omitempty"`
	DeveloperConnection string `json:"developerConnection,omitempty"`
}

type relocation struct {
	GAV     string `json:"gav"`
	Message string `json:"message,omitempty"`
}

type dep struct {
	GroupID    string `json:"groupId"`
	ArtifactID string `json:"artifactId"`
	Version    string `json:"version"`
	Scope      string `json:"scope"`
	Type       string `json:"type"`
	Classifier string `json:"classifier,omitempty"`
	Optional   bool   `json:"optional"`
	Resolution string `json:"resolution"`
	Expression string `json:"expression,omitempty"`
	Profile    string `json:"profile,omitempty"`
}

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "pom:", err)
		os.Exit(1)
	}
}

const (
	profileDefault     = "default"
	profilePessimistic = "pessimistic"
)

func run(args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("pom", flag.ContinueOnError)
	repo := fs.String("repo", pom.DefaultRepoURL, "Maven repository base URL")
	file := fs.String("f", "", "read root POM from file (use - for stdin)")
	profiles := fs.String("profiles", profileDefault, "profile mode: default | pessimistic | id1,id2,...")
	follow := fs.Bool("relocate", false, "follow <relocation> and resolve the target")
	asXML := fs.Bool("xml", false, "emit a minimal effective-pom XML instead of JSON")
	timeout := fs.Duration("timeout", defaultTimeout, "overall timeout")
	version := fs.Bool("version", false, "print version and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *version {
		_, err := fmt.Fprintln(stdout, "pom", pom.Version)
		return err
	}

	opts := pom.Options{Profiles: parseProfiles(*profiles)}
	fetcher := pom.NewCachingFetcher(pom.NewHTTPFetcher(*repo))
	r := pom.NewResolver(fetcher)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	ep, err := resolve(ctx, r, fs.Arg(0), *file, stdin, opts)
	if err != nil {
		return err
	}
	if *follow {
		ep, err = followRelocation(ctx, r, ep, opts)
		if err != nil {
			return err
		}
	}

	if *asXML {
		return writeXML(stdout, ep)
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(render(ep))
}

func resolve(ctx context.Context, r *pom.Resolver, coord, file string, stdin io.Reader, opts pom.Options) (*pom.EffectivePOM, error) {
	switch {
	case file == "-":
		data, err := io.ReadAll(stdin)
		if err != nil {
			return nil, err
		}
		p, err := pom.ParsePOM(data)
		if err != nil {
			return nil, err
		}
		return r.ResolvePOM(ctx, p, opts)
	case file != "":
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		p, err := pom.ParsePOM(data)
		if err != nil {
			return nil, err
		}
		return r.ResolvePOM(ctx, p, opts)
	case coord != "":
		gav, err := pom.ParseGAV(coord)
		if err != nil {
			return nil, err
		}
		return r.Resolve(ctx, gav, opts)
	default:
		return nil, fmt.Errorf("need a coordinate argument or -f")
	}
}

func followRelocation(ctx context.Context, r *pom.Resolver, ep *pom.EffectivePOM, opts pom.Options) (*pom.EffectivePOM, error) {
	seen := map[pom.GAV]bool{ep.GAV: true}
	for range relocateLimit {
		if ep.Relocation == nil {
			return ep, nil
		}
		target := ep.Relocation.Target(ep.GAV)
		if seen[target] {
			return ep, nil
		}
		seen[target] = true
		next, err := r.Resolve(ctx, target, opts)
		if err != nil {
			return nil, fmt.Errorf("follow relocation to %s: %w", target, err)
		}
		ep = next
	}
	return ep, nil
}

func parseProfiles(s string) pom.ProfileActivation {
	switch s {
	case "", profileDefault:
		return pom.ProfileActivation{Mode: pom.OnlyDefault}
	case profilePessimistic, "all":
		return pom.ProfileActivation{Mode: pom.Pessimistic}
	default:
		return pom.ProfileActivation{Mode: pom.Explicit, IDs: strings.Split(s, ",")}
	}
}

func render(ep *pom.EffectivePOM) output {
	out := output{
		GAV:          ep.GAV.String(),
		Packaging:    ep.Packaging,
		Name:         ep.Name,
		Description:  ep.Description,
		URL:          ep.URL,
		Warnings:     ep.Warnings,
		Dependencies: make([]dep, 0, len(ep.Dependencies)),
	}
	for _, l := range ep.Licenses {
		out.Licenses = append(out.Licenses, license{Name: strings.TrimSpace(l.Name), URL: strings.TrimSpace(l.URL)})
	}
	if ep.SCM.URL != "" || ep.SCM.Connection != "" || ep.SCM.DeveloperConnection != "" {
		out.SCM = &scm{URL: ep.SCM.URL, Connection: ep.SCM.Connection, DeveloperConnection: ep.SCM.DeveloperConnection}
	}
	if ep.Relocation != nil {
		out.Relocation = &relocation{GAV: ep.Relocation.Target(ep.GAV).String(), Message: ep.Relocation.Message}
	}
	for _, p := range ep.Parents {
		out.Parents = append(out.Parents, p.String())
	}
	for _, d := range ep.Dependencies {
		out.Dependencies = append(out.Dependencies, dep{
			GroupID:    d.GroupID,
			ArtifactID: d.ArtifactID,
			Version:    d.Version,
			Scope:      d.Scope,
			Type:       d.Type,
			Classifier: d.Classifier,
			Optional:   d.Optional,
			Resolution: string(d.Resolution),
			Expression: d.Expression,
			Profile:    d.Profile,
		})
	}
	return out
}
