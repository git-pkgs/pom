package pom

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// mapFetcher serves POMs from an in-memory map keyed by GAV string.
type mapFetcher map[string]string

func (m mapFetcher) Fetch(_ context.Context, g GAV) (*POM, error) {
	src, ok := m[g.String()]
	if !ok {
		return nil, errors.New("not found")
	}
	return ParsePOM([]byte(src))
}

func resolve(t *testing.T, f Fetcher, gav string, opts Options) *EffectivePOM {
	t.Helper()
	g, _ := ParseGAV(gav)
	ep, err := NewResolver(f).Resolve(context.Background(), g, opts)
	if err != nil {
		t.Fatalf("resolve %s: %v", gav, err)
	}
	return ep
}

func depByGA(ep *EffectivePOM, ga string) *ResolvedDep {
	for i := range ep.Dependencies {
		d := &ep.Dependencies[i]
		if d.GroupID+":"+d.ArtifactID == ga {
			return d
		}
	}
	return nil
}

func TestParentChainMerge(t *testing.T) {
	f := mapFetcher{
		"org.x:grand:1": `<project>
			<groupId>org.x</groupId><artifactId>grand</artifactId><version>1</version>
			<properties><lib.version>1.0</lib.version><other>g</other></properties>
			<dependencyManagement><dependencies>
				<dependency><groupId>org.lib</groupId><artifactId>lib</artifactId><version>${lib.version}</version></dependency>
			</dependencies></dependencyManagement>
		</project>`,
		"org.x:parent:1": `<project>
			<parent><groupId>org.x</groupId><artifactId>grand</artifactId><version>1</version></parent>
			<artifactId>parent</artifactId>
			<properties><lib.version>2.0</lib.version></properties>
			<dependencies>
				<dependency><groupId>org.lib</groupId><artifactId>lib</artifactId></dependency>
			</dependencies>
		</project>`,
		"org.x:child:1": `<project>
			<parent><groupId>org.x</groupId><artifactId>parent</artifactId><version>1</version></parent>
			<artifactId>child</artifactId>
			<dependencies>
				<dependency><groupId>org.x</groupId><artifactId>sibling</artifactId><version>${project.version}</version></dependency>
			</dependencies>
		</project>`,
	}

	ep := resolve(t, f, "org.x:child:1", Options{})

	if len(ep.Parents) != 2 || ep.Parents[0].ArtifactID != "parent" || ep.Parents[1].ArtifactID != "grand" {
		t.Errorf("parent chain: %v", ep.Parents)
	}
	if ep.Properties["lib.version"] != "2.0" {
		t.Errorf("child-wins property: got %q", ep.Properties["lib.version"])
	}
	if ep.Properties["other"] != "g" {
		t.Errorf("inherited property: got %q", ep.Properties["other"])
	}
	if ep.Properties["project.version"] != "1" {
		t.Errorf("synthesised project.version: got %q", ep.Properties["project.version"])
	}

	lib := depByGA(ep, "org.lib:lib")
	if lib == nil || lib.Version != "2.0" || lib.Resolution != Resolved {
		t.Errorf("lib via depMgmt+childprop: %+v", lib)
	}
	sib := depByGA(ep, "org.x:sibling")
	if sib == nil || sib.Version != "1" || sib.Resolution != Resolved {
		t.Errorf("sibling via project.version: %+v", sib)
	}
}

func TestBOMImportFirstWins(t *testing.T) {
	f := mapFetcher{
		"org.x:app:1": `<project>
			<groupId>org.x</groupId><artifactId>app</artifactId><version>1</version>
			<dependencyManagement><dependencies>
				<dependency><groupId>org.bom</groupId><artifactId>a</artifactId><version>1</version><type>pom</type><scope>import</scope></dependency>
				<dependency><groupId>org.bom</groupId><artifactId>b</artifactId><version>1</version><type>pom</type><scope>import</scope></dependency>
				<dependency><groupId>org.lib</groupId><artifactId>pinned</artifactId><version>9.9</version></dependency>
			</dependencies></dependencyManagement>
			<dependencies>
				<dependency><groupId>org.lib</groupId><artifactId>shared</artifactId></dependency>
				<dependency><groupId>org.lib</groupId><artifactId>only-b</artifactId></dependency>
				<dependency><groupId>org.lib</groupId><artifactId>pinned</artifactId></dependency>
			</dependencies>
		</project>`,
		"org.bom:a:1": `<project>
			<groupId>org.bom</groupId><artifactId>a</artifactId><version>1</version>
			<dependencyManagement><dependencies>
				<dependency><groupId>org.lib</groupId><artifactId>shared</artifactId><version>1.0</version></dependency>
				<dependency><groupId>org.lib</groupId><artifactId>pinned</artifactId><version>1.0</version></dependency>
			</dependencies></dependencyManagement>
		</project>`,
		"org.bom:b:1": `<project>
			<groupId>org.bom</groupId><artifactId>b</artifactId><version>1</version>
			<dependencyManagement><dependencies>
				<dependency><groupId>org.lib</groupId><artifactId>shared</artifactId><version>2.0</version></dependency>
				<dependency><groupId>org.lib</groupId><artifactId>only-b</artifactId><version>2.0</version></dependency>
			</dependencies></dependencyManagement>
		</project>`,
	}

	ep := resolve(t, f, "org.x:app:1", Options{})

	if d := depByGA(ep, "org.lib:shared"); d == nil || d.Version != "1.0" {
		t.Errorf("first-declared BOM should win: %+v", d)
	}
	if d := depByGA(ep, "org.lib:only-b"); d == nil || d.Version != "2.0" {
		t.Errorf("second BOM contributes non-conflicting: %+v", d)
	}
	if d := depByGA(ep, "org.lib:pinned"); d == nil || d.Version != "9.9" {
		t.Errorf("explicit depMgmt entry beats BOM: %+v", d)
	}
}

func TestResolutionTags(t *testing.T) {
	f := mapFetcher{
		"org.x:app:1": `<project>
			<groupId>org.x</groupId><artifactId>app</artifactId><version>1</version>
			<dependencies>
				<dependency><groupId>a</groupId><artifactId>ok</artifactId><version>1.0</version></dependency>
				<dependency><groupId>a</groupId><artifactId>prop</artifactId><version>${nope}</version></dependency>
				<dependency><groupId>a</groupId><artifactId>env</artifactId><version>${env.BUILD}</version></dependency>
				<dependency><groupId>a</groupId><artifactId>missing</artifactId></dependency>
				<dependency><groupId>a</groupId><artifactId>gated</artifactId><version>${gated.version}</version></dependency>
			</dependencies>
			<profiles>
				<profile><id>p</id><properties><gated.version>3.0</gated.version></properties></profile>
			</profiles>
		</project>`,
	}

	ep := resolve(t, f, "org.x:app:1", Options{Profiles: ProfileActivation{Mode: OnlyDefault}})

	tests := []struct {
		ga   string
		ver  string
		res  Resolution
		expr string
	}{
		{"a:ok", "1.0", Resolved, ""},
		{"a:prop", "${nope}", UnresolvedProperty, "${nope}"},
		{"a:env", "${env.BUILD}", UnresolvedEnv, "${env.BUILD}"},
		{"a:missing", "", UnresolvedMissing, ""},
		{"a:gated", "${gated.version}", UnresolvedProfileGated, "${gated.version}"},
	}
	for _, tt := range tests {
		d := depByGA(ep, tt.ga)
		if d == nil {
			t.Errorf("%s: not found", tt.ga)
			continue
		}
		if d.Version != tt.ver || d.Resolution != tt.res || d.Expression != tt.expr {
			t.Errorf("%s: got version=%q res=%q expr=%q want version=%q res=%q expr=%q",
				tt.ga, d.Version, d.Resolution, d.Expression, tt.ver, tt.res, tt.expr)
		}
	}
}

func TestUnresolvedParent(t *testing.T) {
	f := mapFetcher{
		"org.x:child:1": `<project>
			<parent><groupId>org.x</groupId><artifactId>ghost</artifactId><version>1</version></parent>
			<artifactId>child</artifactId>
			<dependencies>
				<dependency><groupId>a</groupId><artifactId>noversion</artifactId></dependency>
				<dependency><groupId>a</groupId><artifactId>prop</artifactId><version>${maybe.in.parent}</version></dependency>
			</dependencies>
		</project>`,
	}

	ep := resolve(t, f, "org.x:child:1", Options{})

	if len(ep.Warnings) == 0 {
		t.Error("expected fetch-parent warning")
	}
	for _, ga := range []string{"a:noversion", "a:prop"} {
		d := depByGA(ep, ga)
		if d == nil || d.Resolution != UnresolvedParent {
			t.Errorf("%s: want UnresolvedParent, got %+v", ga, d)
		}
	}
}

func TestProfileActivationModes(t *testing.T) {
	src := `<project>
		<groupId>org.x</groupId><artifactId>app</artifactId><version>1</version>
		<dependencies>
			<dependency><groupId>a</groupId><artifactId>base</artifactId><version>1</version></dependency>
		</dependencies>
		<profiles>
			<profile>
				<id>def</id>
				<activation><activeByDefault>true</activeByDefault></activation>
				<dependencies><dependency><groupId>a</groupId><artifactId>def</artifactId><version>1</version></dependency></dependencies>
			</profile>
			<profile>
				<id>opt</id>
				<dependencies><dependency><groupId>a</groupId><artifactId>opt</artifactId><version>1</version></dependency></dependencies>
			</profile>
		</profiles>
	</project>`
	f := mapFetcher{"org.x:app:1": src}

	tests := []struct {
		name string
		act  ProfileActivation
		want []string
	}{
		{"default", ProfileActivation{Mode: OnlyDefault}, []string{"a:base", "a:def"}},
		{"pessimistic", ProfileActivation{Mode: Pessimistic}, []string{"a:base", "a:def", "a:opt"}},
		{"explicit", ProfileActivation{Mode: Explicit, IDs: []string{"opt"}}, []string{"a:base", "a:def", "a:opt"}},
		{"explicit-none", ProfileActivation{Mode: Explicit, IDs: nil}, []string{"a:base", "a:def"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ep := resolve(t, f, "org.x:app:1", Options{Profiles: tt.act})
			var got []string
			for _, d := range ep.Dependencies {
				got = append(got, d.GroupID+":"+d.ArtifactID)
			}
			slices.Sort(got)
			slices.Sort(tt.want)
			if !slices.Equal(got, tt.want) {
				t.Errorf("got %v want %v", got, tt.want)
			}
			for _, d := range ep.Dependencies {
				if d.ArtifactID == "opt" && d.Profile != "opt" {
					t.Errorf("opt dep should be tagged with profile, got %q", d.Profile)
				}
			}
		})
	}
}

func TestParentCycle(t *testing.T) {
	f := mapFetcher{
		"org.x:a:1": `<project><parent><groupId>org.x</groupId><artifactId>b</artifactId><version>1</version></parent><artifactId>a</artifactId></project>`,
		"org.x:b:1": `<project><parent><groupId>org.x</groupId><artifactId>a</artifactId><version>1</version></parent><artifactId>b</artifactId></project>`,
	}
	ep := resolve(t, f, "org.x:a:1", Options{})
	if len(ep.Warnings) == 0 {
		t.Error("expected cycle warning")
	}
}

func TestResolvePOMDirect(t *testing.T) {
	root, _ := ParsePOM([]byte(`<project>
		<groupId>org.x</groupId><artifactId>local</artifactId><version>1.0</version>
		<dependencies><dependency><groupId>a</groupId><artifactId>b</artifactId><version>${project.version}</version></dependency></dependencies>
	</project>`))
	r := NewResolver(mapFetcher{})
	ep, err := r.ResolvePOM(context.Background(), root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if d := depByGA(ep, "a:b"); d == nil || d.Version != "1.0" {
		t.Errorf("ResolvePOM: %+v", d)
	}
}

func TestDepOverlayAcrossChain(t *testing.T) {
	f := mapFetcher{
		"org.x:parent:1": `<project>
			<groupId>org.x</groupId><artifactId>parent</artifactId><version>1</version>
			<dependencies>
				<dependency>
					<groupId>a</groupId><artifactId>b</artifactId><version>1.0</version>
					<scope>compile</scope><optional>false</optional>
				</dependency>
			</dependencies>
		</project>`,
		"org.x:child:1": `<project>
			<parent><groupId>org.x</groupId><artifactId>parent</artifactId><version>1</version></parent>
			<artifactId>child</artifactId>
			<dependencies>
				<dependency>
					<groupId>a</groupId><artifactId>b</artifactId><version>2.0</version>
					<scope>test</scope><optional>true</optional>
					<exclusions><exclusion><groupId>x</groupId><artifactId>y</artifactId></exclusion></exclusions>
				</dependency>
			</dependencies>
		</project>`,
	}
	ep := resolve(t, f, "org.x:child:1", Options{})
	d := depByGA(ep, "a:b")
	if d == nil || d.Version != "2.0" || d.Scope != "test" || !d.Optional || len(d.Exclusions) != 1 {
		t.Errorf("child should override parent dep fields: %+v", d)
	}
	if len(ep.Dependencies) != 1 {
		t.Errorf("dep should be merged not duplicated: got %d", len(ep.Dependencies))
	}
}

func TestBOMUnresolvableAndMissing(t *testing.T) {
	f := mapFetcher{
		"org.x:app:1": `<project>
			<groupId>org.x</groupId><artifactId>app</artifactId><version>1</version>
			<dependencyManagement><dependencies>
				<dependency><groupId>org.bom</groupId><artifactId>noversion</artifactId><version>${nope}</version><type>pom</type><scope>import</scope></dependency>
				<dependency><groupId>org.bom</groupId><artifactId>ghost</artifactId><version>1</version><type>pom</type><scope>import</scope></dependency>
			</dependencies></dependencyManagement>
		</project>`,
	}
	ep := resolve(t, f, "org.x:app:1", Options{})
	if len(ep.Warnings) != 2 {
		t.Errorf("want 2 warnings (unresolvable + fetch fail), got %v", ep.Warnings)
	}
}

func TestResolverCacheAndError(t *testing.T) {
	r := NewResolver(mapFetcher{
		"org.x:a:1": `<project><groupId>org.x</groupId><artifactId>a</artifactId><version>1</version></project>`,
	})
	ctx := context.Background()
	g := GAV{"org.x", "a", "1"}
	ep1, err := r.Resolve(ctx, g, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ep2, _ := r.Resolve(ctx, g, Options{})
	if ep1 != ep2 {
		t.Error("second resolve should hit cache")
	}
	if _, err := r.Resolve(ctx, GAV{"org.x", "ghost", "1"}, Options{}); err == nil {
		t.Error("expected error for unfetchable root")
	}
}

func TestLookupManagedFallback(t *testing.T) {
	f := mapFetcher{
		"org.x:app:1": `<project>
			<groupId>org.x</groupId><artifactId>app</artifactId><version>1</version>
			<dependencyManagement><dependencies>
				<dependency><groupId>a</groupId><artifactId>b</artifactId><version>1.0</version></dependency>
			</dependencies></dependencyManagement>
			<dependencies>
				<dependency><groupId>a</groupId><artifactId>b</artifactId><type>test-jar</type></dependency>
			</dependencies>
		</project>`,
	}
	ep := resolve(t, f, "org.x:app:1", Options{})
	d := depByGA(ep, "a:b")
	if d == nil || d.Version != "1.0" || d.Type != "test-jar" {
		t.Errorf("fallback to plain g:a managed entry: %+v", d)
	}
}

func TestMetadataMerge(t *testing.T) {
	f := mapFetcher{
		"org.x:parent:1": `<project>
			<groupId>org.x</groupId><artifactId>parent</artifactId><version>1</version>
			<name>Parent</name>
			<description>parent desc</description>
			<url>https://parent.example</url>
			<licenses><license><name>MIT</name><url>https://mit</url></license></licenses>
			<scm><url>https://scm.parent</url><connection>scm:git:parent</connection></scm>
		</project>`,
		"org.x:child:1": `<project>
			<parent><groupId>org.x</groupId><artifactId>parent</artifactId><version>1</version></parent>
			<artifactId>child</artifactId>
			<name>${project.artifactId}</name>
			<scm><url>https://scm.child/${project.artifactId}</url></scm>
		</project>`,
	}
	ep := resolve(t, f, "org.x:child:1", Options{})
	if ep.Name != "child" {
		t.Errorf("name interpolated: got %q", ep.Name)
	}
	if ep.Description != "parent desc" {
		t.Errorf("description inherited: got %q", ep.Description)
	}
	if ep.URL != "https://parent.example" {
		t.Errorf("url inherited: got %q", ep.URL)
	}
	if len(ep.Licenses) != 1 || ep.Licenses[0].Name != "MIT" {
		t.Errorf("licenses inherited: %+v", ep.Licenses)
	}
	if ep.SCM.URL != "https://scm.child/child" {
		t.Errorf("scm child-wins + interpolated: got %q", ep.SCM.URL)
	}
}

func TestRelocation(t *testing.T) {
	p, err := ParsePOM([]byte(`<project>
		<groupId>old</groupId><artifactId>thing</artifactId><version>1.0</version>
		<distributionManagement><relocation>
			<groupId>new</groupId>
			<message>moved</message>
		</relocation></distributionManagement>
	</project>`))
	if err != nil {
		t.Fatal(err)
	}
	r := p.DistributionManagement.Relocation
	if r == nil {
		t.Fatal("relocation not parsed")
	}
	want := GAV{"new", "thing", "1.0"}
	if got := r.Target(p.EffectiveGAV()); got != want {
		t.Errorf("Target: got %v want %v", got, want)
	}
	ep, _ := NewResolver(mapFetcher{}).ResolvePOM(context.Background(), p, Options{})
	if ep.Relocation == nil || ep.Relocation.Message != "moved" {
		t.Errorf("relocation on EffectivePOM: %+v", ep.Relocation)
	}
}

func TestAccessors(t *testing.T) {
	g := GAV{"a", "b", "1"}
	if g.GA() != "a:b" {
		t.Errorf("GA: %q", g.GA())
	}
	d := Dep{GroupID: "a", ArtifactID: "b", Version: "1"}
	if d.GAV() != g {
		t.Errorf("Dep.GAV: %v", d.GAV())
	}
	rd := ResolvedDep{GroupID: "a", ArtifactID: "b", Version: "1"}
	if rd.GAV() != g {
		t.Errorf("ResolvedDep.GAV: %v", rd.GAV())
	}
}

func TestManagedScopeAndExclusions(t *testing.T) {
	f := mapFetcher{
		"org.x:app:1": `<project>
			<groupId>org.x</groupId><artifactId>app</artifactId><version>1</version>
			<dependencyManagement><dependencies>
				<dependency>
					<groupId>a</groupId><artifactId>b</artifactId><version>1.0</version>
					<scope>runtime</scope>
					<exclusions><exclusion><groupId>x</groupId><artifactId>y</artifactId></exclusion></exclusions>
				</dependency>
			</dependencies></dependencyManagement>
			<dependencies>
				<dependency><groupId>a</groupId><artifactId>b</artifactId></dependency>
			</dependencies>
		</project>`,
	}
	ep := resolve(t, f, "org.x:app:1", Options{})
	d := depByGA(ep, "a:b")
	if d == nil || d.Version != "1.0" || d.Scope != "runtime" || len(d.Exclusions) != 1 {
		t.Errorf("managed scope/exclusions: %+v", d)
	}
}
