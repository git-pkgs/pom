package pom

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
)

const (
	maxParentDepth = 32
	maxBOMDepth    = 16
)

// Resolution classifies how (or whether) a dependency's version was
// determined. Missing-version and unresolved-property are the same
// underlying problem so they share this taxonomy.
type Resolution string

const (
	// Resolved means a concrete version string was produced.
	Resolved Resolution = "resolved"
	// UnresolvedProperty means a ${name} expression remained after
	// interpolation and no source defines it.
	UnresolvedProperty Resolution = "unresolved_property"
	// UnresolvedEnv means the version references ${env.X} which can never
	// be resolved statically.
	UnresolvedEnv Resolution = "unresolved_env"
	// UnresolvedParent means a parent POM in the chain could not be
	// fetched, so the whole resolution is suspect.
	UnresolvedParent Resolution = "unresolved_parent"
	// UnresolvedProfileGated means the version is only defined inside a
	// profile that was not activated under the current mode.
	UnresolvedProfileGated Resolution = "unresolved_profile_gated"
	// UnresolvedMissing means no version was declared anywhere reachable:
	// not on the dependency, not in dependencyManagement, not in any BOM.
	UnresolvedMissing Resolution = "unresolved_missing"
)

// ProfileMode controls which <profile> sections contribute to the merge.
type ProfileMode int

const (
	// OnlyDefault activates only profiles with <activeByDefault>true</activeByDefault>.
	OnlyDefault ProfileMode = iota
	// Pessimistic activates every profile, on the basis that for vuln
	// scanning a false positive is preferable to a false negative.
	Pessimistic
	// Explicit activates only the named profile IDs (plus activeByDefault).
	Explicit
)

// ProfileActivation configures profile selection for a Resolve call.
type ProfileActivation struct {
	Mode ProfileMode
	IDs  []string
}

// Fetcher retrieves a parsed POM for a coordinate. Implementations are
// expected to be safe for concurrent use; the resolver itself is
// synchronous but callers may share a Fetcher across goroutines.
type Fetcher interface {
	Fetch(ctx context.Context, gav GAV) (*POM, error)
}

// Resolver computes effective POMs. It owns a Fetcher and uses it for the
// root, every parent in the chain, and every imported BOM, so all I/O goes
// through one place.
type Resolver struct {
	fetcher Fetcher
	cache   map[GAV]*EffectivePOM
}

// NewResolver constructs a Resolver around f. Resolved POMs are memoised
// for the lifetime of the Resolver since released coordinates are
// immutable.
func NewResolver(f Fetcher) *Resolver {
	return &Resolver{fetcher: f, cache: map[GAV]*EffectivePOM{}}
}

// Options tunes a single Resolve call.
type Options struct {
	Profiles ProfileActivation
}

// EffectivePOM is the merged, interpolated view of a coordinate.
type EffectivePOM struct {
	GAV GAV

	Packaging   string
	Name        string
	Description string
	URL         string
	Licenses    []License
	SCM         SCM

	// Relocation is set when the root POM declares a
	// <distributionManagement><relocation>. Callers may want to follow it
	// and re-resolve.
	Relocation *Relocation

	// Properties is the merged property map after parent inheritance,
	// profile contribution, project.* synthesis, and self-interpolation.
	Properties map[string]string

	// Dependencies are the project's direct dependencies with versions
	// filled from dependencyManagement and interpolated.
	Dependencies []ResolvedDep

	// DependencyManagement is the merged managed-dependency table, after
	// BOM expansion, keyed by management key.
	DependencyManagement map[string]Dep

	// Parents lists the parent chain from immediate parent to root.
	Parents []GAV

	// ActiveProfiles lists profile IDs that contributed to the merge.
	ActiveProfiles []string

	// Warnings collects non-fatal issues encountered during resolution
	// (parent fetch failures, BOM fetch failures, depth limits).
	Warnings []string
}

// ResolvedDep is a dependency after merge and interpolation, tagged with
// how its version was (or wasn't) determined.
type ResolvedDep struct {
	GroupID    string
	ArtifactID string
	Version    string
	Type       string
	Classifier string
	Scope      string
	Optional   bool
	Exclusions []Exclusion

	Resolution Resolution
	// Expression holds the original unresolved ${...} string when
	// Resolution is one of the unresolved_* values.
	Expression string
	// Profile is set when this dependency was contributed by a profile.
	Profile string
}

func (d ResolvedDep) GAV() GAV {
	return GAV{GroupID: d.GroupID, ArtifactID: d.ArtifactID, Version: d.Version}
}

// Resolve fetches gav and computes its effective POM under opts.
func (r *Resolver) Resolve(ctx context.Context, gav GAV, opts Options) (*EffectivePOM, error) {
	if ep, ok := r.cache[gav]; ok {
		return ep, nil
	}
	root, err := r.fetcher.Fetch(ctx, gav)
	if err != nil {
		return nil, fmt.Errorf("pom: fetch %s: %w", gav, err)
	}
	ep, err := r.ResolvePOM(ctx, root, opts)
	if err != nil {
		return nil, err
	}
	r.cache[gav] = ep
	return ep, nil
}

// ResolvePOM computes the effective POM for an already-parsed root POM.
// Useful when the caller holds a pom.xml from a source checkout that is
// not itself fetchable by coordinate.
func (r *Resolver) ResolvePOM(ctx context.Context, root *POM, opts Options) (*EffectivePOM, error) {
	chain, warnings := r.parentChain(ctx, root)

	m := newMerger(opts.Profiles)
	parentFailed := len(warnings) > 0
	for _, p := range chain {
		m.apply(p)
	}

	rootGAV := root.EffectiveGAV()
	m.synthesiseProjectProps(root, rootGAV)
	m.interpolateProps()

	bomWarnings := r.expandBOMs(ctx, m, 0)
	warnings = append(warnings, bomWarnings...)

	m.interpolateDepMgmt()

	deps := m.resolveDeps(parentFailed)

	ep := &EffectivePOM{
		GAV:                  rootGAV,
		Packaging:            defaultType(root.Packaging),
		Name:                 interpolate(m.meta.name, m.props),
		Description:          interpolate(strings.TrimSpace(m.meta.description), m.props),
		URL:                  interpolate(m.meta.url, m.props),
		Licenses:             m.meta.licenses,
		SCM:                  m.interpolateSCM(),
		Relocation:           root.DistributionManagement.Relocation,
		Properties:           m.props,
		Dependencies:         deps,
		DependencyManagement: m.depMgmt,
		Parents:              parentsOf(chain),
		ActiveProfiles:       m.activeProfiles,
		Warnings:             warnings,
	}
	return ep, nil
}

// parentChain fetches the inheritance chain and returns it ordered root
// first, child last, so a linear merge naturally gives child-wins.
func (r *Resolver) parentChain(ctx context.Context, root *POM) ([]*POM, []string) {
	chain := []*POM{root}
	var warnings []string
	seen := map[GAV]bool{root.EffectiveGAV(): true}
	cur := root
	for depth := 0; cur.Parent != nil; depth++ {
		if depth >= maxParentDepth {
			warnings = append(warnings, fmt.Sprintf("parent chain exceeds %d levels at %s", maxParentDepth, cur.Parent.GAV()))
			break
		}
		pgav := cur.Parent.GAV()
		if seen[pgav] {
			warnings = append(warnings, fmt.Sprintf("parent cycle detected at %s", pgav))
			break
		}
		seen[pgav] = true
		p, err := r.fetcher.Fetch(ctx, pgav)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("fetch parent %s: %v", pgav, err))
			break
		}
		chain = append(chain, p)
		cur = p
	}
	slices.Reverse(chain)
	return chain, warnings
}

func parentsOf(chain []*POM) []GAV {
	if len(chain) <= 1 {
		return nil
	}
	parents := chain[:len(chain)-1]
	out := make([]GAV, 0, len(parents))
	for i := len(parents) - 1; i >= 0; i-- {
		out = append(out, parents[i].EffectiveGAV())
	}
	return out
}

// expandBOMs recursively resolves <scope>import</scope> entries in the
// merged dependencyManagement, replacing each with the managed entries of
// the imported BOM. First-declared wins on conflict.
func (r *Resolver) expandBOMs(ctx context.Context, m *merger, depth int) []string {
	var warnings []string
	if depth >= maxBOMDepth {
		return []string{fmt.Sprintf("BOM import depth exceeds %d", maxBOMDepth)}
	}
	imports := m.takeImports()
	for _, imp := range imports {
		gav := GAV{
			GroupID:    interpolate(imp.GroupID, m.props),
			ArtifactID: interpolate(imp.ArtifactID, m.props),
			Version:    interpolate(imp.Version, m.props),
		}
		if containsExpr(gav.Version) || gav.Version == "" {
			warnings = append(warnings, fmt.Sprintf("BOM import %s:%s has unresolvable version %q", gav.GroupID, gav.ArtifactID, imp.Version))
			continue
		}
		ep, err := r.resolveBOM(ctx, gav, depth+1)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("fetch BOM %s: %v", gav, err))
			continue
		}
		m.importBOM(ep.DependencyManagement)
		warnings = append(warnings, ep.Warnings...)
	}
	return warnings
}

func (r *Resolver) resolveBOM(ctx context.Context, gav GAV, depth int) (*EffectivePOM, error) {
	if ep, ok := r.cache[gav]; ok {
		return ep, nil
	}
	root, err := r.fetcher.Fetch(ctx, gav)
	if err != nil {
		return nil, err
	}
	chain, warnings := r.parentChain(ctx, root)
	m := newMerger(ProfileActivation{Mode: OnlyDefault})
	for _, p := range chain {
		m.apply(p)
	}
	m.synthesiseProjectProps(root, root.EffectiveGAV())
	m.interpolateProps()
	warnings = append(warnings, r.expandBOMs(ctx, m, depth)...)
	m.interpolateDepMgmt()
	ep := &EffectivePOM{
		GAV:                  root.EffectiveGAV(),
		Properties:           m.props,
		DependencyManagement: m.depMgmt,
		Warnings:             warnings,
	}
	r.cache[gav] = ep
	return ep, nil
}

type metadata struct {
	name        string
	description string
	url         string
	licenses    []License
	scm         SCM
}

// merger accumulates state across the parent chain.
type merger struct {
	activation ProfileActivation

	props   map[string]string
	meta    metadata
	depMgmt map[string]Dep
	imports []Dep

	deps     []Dep
	depKeys  map[string]int
	depProf  map[string]string
	profDefs map[string]bool

	activeProfiles []string
}

func newMerger(act ProfileActivation) *merger {
	return &merger{
		activation: act,
		props:      map[string]string{},
		depMgmt:    map[string]Dep{},
		depKeys:    map[string]int{},
		depProf:    map[string]string{},
		profDefs:   map[string]bool{},
	}
}

// apply merges one POM into the accumulator. Called root-first, so later
// calls (children) overwrite earlier ones (parents).
func (m *merger) apply(p *POM) {
	maps.Copy(m.props, p.Properties)
	m.mergeMeta(p)
	m.mergeDepMgmt(p.DependencyManagement.Dependencies, true)
	m.mergeDeps(p.Dependencies, "")

	for i := range p.Profiles {
		pr := &p.Profiles[i]
		active := m.activation.active(pr)
		if !active {
			m.recordProfileGated(pr)
			continue
		}
		m.activeProfiles = append(m.activeProfiles, pr.ID)
		maps.Copy(m.props, pr.Properties)
		m.mergeDepMgmt(pr.DependencyManagement.Dependencies, true)
		m.mergeDeps(pr.Dependencies, pr.ID)
	}
}

func (a ProfileActivation) active(p *Profile) bool {
	def := strings.EqualFold(strings.TrimSpace(p.Activation.ActiveByDefault), "true")
	switch a.Mode {
	case Pessimistic:
		return true
	case Explicit:
		return def || slices.Contains(a.IDs, p.ID)
	case OnlyDefault:
		fallthrough
	default:
		return def
	}
}

func (m *merger) mergeMeta(p *POM) {
	if p.Name != "" {
		m.meta.name = p.Name
	}
	if strings.TrimSpace(p.Description) != "" {
		m.meta.description = p.Description
	}
	if p.URL != "" {
		m.meta.url = p.URL
	}
	if len(p.Licenses) > 0 {
		m.meta.licenses = p.Licenses
	}
	if !p.SCM.empty() {
		m.meta.scm = p.SCM
	}
}

func (m *merger) interpolateSCM() SCM {
	return SCM{
		URL:                 interpolate(m.meta.scm.URL, m.props),
		Connection:          interpolate(m.meta.scm.Connection, m.props),
		DeveloperConnection: interpolate(m.meta.scm.DeveloperConnection, m.props),
	}
}

func (m *merger) recordProfileGated(pr *Profile) {
	for k := range pr.Properties {
		m.profDefs[k] = true
	}
}

// mergeDepMgmt folds managed entries into the accumulator. childWins
// controls precedence: true for parent->child inheritance (child overrides
// parent), false for BOM imports (existing entry wins).
func (m *merger) mergeDepMgmt(entries []Dep, childWins bool) {
	for _, d := range entries {
		if d.Scope == scopeImport {
			m.imports = append(m.imports, d)
			continue
		}
		k := d.managementKey()
		if !childWins {
			if _, exists := m.depMgmt[k]; exists {
				continue
			}
		}
		m.depMgmt[k] = d
	}
}

func (m *merger) mergeDeps(entries []Dep, profile string) {
	for _, d := range entries {
		k := d.managementKey()
		if i, ok := m.depKeys[k]; ok {
			m.deps[i] = overlayDep(m.deps[i], d)
			if profile != "" {
				m.depProf[k] = profile
			}
			continue
		}
		m.depKeys[k] = len(m.deps)
		m.deps = append(m.deps, d)
		if profile != "" {
			m.depProf[k] = profile
		}
	}
}

func overlayDep(base, over Dep) Dep {
	if over.Version != "" {
		base.Version = over.Version
	}
	if over.Scope != "" {
		base.Scope = over.Scope
	}
	if over.Optional != "" {
		base.Optional = over.Optional
	}
	if len(over.Exclusions) > 0 {
		base.Exclusions = over.Exclusions
	}
	return base
}

func (m *merger) takeImports() []Dep {
	out := m.imports
	m.imports = nil
	return out
}

func (m *merger) importBOM(managed map[string]Dep) {
	for k, d := range managed {
		if _, exists := m.depMgmt[k]; exists {
			continue
		}
		m.depMgmt[k] = d
	}
}

func (m *merger) synthesiseProjectProps(root *POM, gav GAV) {
	set := func(k, v string) {
		if v == "" {
			return
		}
		if _, ok := m.props[k]; !ok {
			m.props[k] = v
		}
	}
	set("project.groupId", gav.GroupID)
	set("project.artifactId", gav.ArtifactID)
	set("project.version", gav.Version)
	if root.Parent != nil {
		set("project.parent.groupId", root.Parent.GroupID)
		set("project.parent.artifactId", root.Parent.ArtifactID)
		set("project.parent.version", root.Parent.Version)
	}
	if root.Packaging != "" {
		set("project.packaging", root.Packaging)
	}
}

func (m *merger) interpolateProps() {
	for range maxInterpolationPasses {
		changed := false
		for k, v := range m.props {
			nv := interpolate(v, m.props)
			if nv != v {
				m.props[k] = nv
				changed = true
			}
		}
		if !changed {
			break
		}
	}
}

func (m *merger) interpolateDepMgmt() {
	out := make(map[string]Dep, len(m.depMgmt))
	for _, d := range m.depMgmt {
		d.GroupID = interpolate(d.GroupID, m.props)
		d.ArtifactID = interpolate(d.ArtifactID, m.props)
		d.Version = interpolate(d.Version, m.props)
		d.Scope = interpolate(d.Scope, m.props)
		d.Type = interpolate(d.Type, m.props)
		d.Classifier = interpolate(d.Classifier, m.props)
		out[d.managementKey()] = d
	}
	m.depMgmt = out
}

func (m *merger) resolveDeps(parentFailed bool) []ResolvedDep {
	out := make([]ResolvedDep, 0, len(m.deps))
	for _, d := range m.deps {
		rd := m.resolveDep(d, parentFailed)
		out = append(out, rd)
	}
	return out
}

func (m *merger) resolveDep(d Dep, parentFailed bool) ResolvedDep {
	rawKey := d.managementKey()
	d.GroupID = interpolate(d.GroupID, m.props)
	d.ArtifactID = interpolate(d.ArtifactID, m.props)
	d.Type = interpolate(d.Type, m.props)
	d.Classifier = interpolate(d.Classifier, m.props)
	rawVersion := d.Version

	managed, haveManaged := m.lookupManaged(d)
	if d.Version == "" && haveManaged {
		d.Version = managed.Version
	}
	if d.Scope == "" && haveManaged && managed.Scope != "" {
		d.Scope = managed.Scope
	}
	if len(d.Exclusions) == 0 && haveManaged && len(managed.Exclusions) > 0 {
		d.Exclusions = managed.Exclusions
	}

	d.Version = interpolate(d.Version, m.props)
	d.Scope = interpolate(d.Scope, m.props)

	rd := ResolvedDep{
		GroupID:    d.GroupID,
		ArtifactID: d.ArtifactID,
		Version:    d.Version,
		Type:       defaultType(d.Type),
		Classifier: d.Classifier,
		Scope:      defaultScope(d.Scope),
		Optional:   strings.EqualFold(strings.TrimSpace(d.Optional), "true"),
		Exclusions: d.Exclusions,
		Profile:    m.depProf[rawKey],
	}

	rd.Resolution, rd.Expression = m.classify(d, rawVersion, parentFailed)
	return rd
}

func (m *merger) lookupManaged(d Dep) (Dep, bool) {
	if md, ok := m.depMgmt[d.managementKey()]; ok {
		return md, true
	}
	if d.Type == "" && d.Classifier == "" {
		return Dep{}, false
	}
	fallback := Dep{GroupID: d.GroupID, ArtifactID: d.ArtifactID}
	md, ok := m.depMgmt[fallback.managementKey()]
	return md, ok
}

func (m *merger) classify(d Dep, rawVersion string, parentFailed bool) (Resolution, string) {
	switch {
	case d.Version == "" && parentFailed:
		return UnresolvedParent, rawVersion
	case d.Version == "":
		return UnresolvedMissing, rawVersion
	case containsExpr(d.Version):
		return m.classifyExpr(d.Version, parentFailed)
	}
	for _, f := range []string{d.GroupID, d.ArtifactID, d.Classifier} {
		if containsExpr(f) {
			return m.classifyExpr(f, parentFailed)
		}
	}
	return Resolved, ""
}

func (m *merger) classifyExpr(s string, parentFailed bool) (Resolution, string) {
	name := firstExpr(s)
	switch {
	case strings.HasPrefix(name, "env."):
		return UnresolvedEnv, s
	case m.profDefs[name]:
		return UnresolvedProfileGated, s
	case parentFailed:
		return UnresolvedParent, s
	default:
		return UnresolvedProperty, s
	}
}

func defaultType(t string) string {
	if t == "" {
		return "jar"
	}
	return t
}

func defaultScope(s string) string {
	if s == "" {
		return "compile"
	}
	return s
}
