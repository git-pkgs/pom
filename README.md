# pom

Pure-Go effective-POM resolution for Maven artifacts. No JVM, no shelling out to `mvn`.

This computes the subset of `mvn help:effective-pom` that matters for dependency analysis: walk the parent chain, merge `<properties>` and `<dependencyManagement>`, expand `<scope>import</scope>` BOMs, apply profiles, interpolate `${...}`, and fill in missing versions. It does not touch plugins, lifecycle, or build configuration.

The motivating use case is vulnerability matching, where a dependency declared as `<version>${jackson.version}</version>` is useless until something resolves the property. See [scrutineer#46](https://github.com/alpha-omega-security/scrutineer/issues/46).

## Install

```
go get github.com/git-pkgs/pom
```

Stdlib only, no transitive dependencies.

## Usage

```go
import "github.com/git-pkgs/pom"

fetcher := pom.NewCachingFetcher(pom.NewHTTPFetcher("")) // "" = Maven Central
r := pom.NewResolver(fetcher)

ep, err := r.Resolve(ctx, pom.GAV{
    GroupID:    "com.fasterxml.jackson.core",
    ArtifactID: "jackson-databind",
    Version:    "2.17.2",
}, pom.Options{})

for _, d := range ep.Dependencies {
    fmt.Printf("%s:%s:%s (%s) [%s]\n",
        d.GroupID, d.ArtifactID, d.Version, d.Scope, d.Resolution)
}
```

If you already have a `pom.xml` in hand (from a source checkout, say) use `ResolvePOM`:

```go
p, _ := pom.ParsePOM(bytes)
ep, _ := r.ResolvePOM(ctx, p, pom.Options{})
```

## CLI

`pom` is a small binary that wraps the resolver and prints JSON. It exists so non-Go callers can replace `mvn help:effective-pom` without a JVM.

```
go install github.com/git-pkgs/pom/cmd/pom@latest
```

Resolve by coordinate (fetches the root and its parent chain from the repository):

```
pom com.fasterxml.jackson.core:jackson-databind:2.17.2
```

Or feed POM bytes you already have on stdin, which is what you want when wrapping an existing HTTP fetch:

```
curl -fsSL https://repo1.maven.org/maven2/.../foo-1.0.pom | pom -f -
```

Output is one JSON object with `gav`, `packaging`, `name`, `description`, `url`, `licenses`, `scm`, `relocation`, `parents`, `dependencies` (each tagged with `resolution`), and `warnings`. Pass `-relocate` to follow `<distributionManagement><relocation>` and resolve the target instead, `-repo URL` for a non-Central repository, and `-profiles pessimistic` or `-profiles id1,id2` to control profile activation.

On an M1 the compiled binary resolves jackson-databind (four parents plus a BOM import, 12 dependencies) in ~380 ms wall time of which essentially all is network round-trips to Central; CPU time is under a millisecond. The same artifact through `mvn help:effective-pom` is ~1.7 s and ~150 MB resident.

## Fetchers

`Resolver` takes anything that satisfies one method:

```go
type Fetcher interface {
    Fetch(ctx context.Context, gav GAV) (*POM, error)
}
```

Three implementations ship in the box. `HTTPFetcher` reads from a Maven repository layout. `DirFetcher` reads from a flat directory of `groupId_artifactId_version.pom` files and is what the offline tests use. `CachingFetcher` wraps another fetcher and memoises by GAV, which you almost always want since released coordinates are immutable and parent POMs are heavily shared across a corpus.

If you have your own storage (the `proxy` cache, an S3 bucket, whatever) implement `Fetch` and pass it in.

## Resolution tags

Every `ResolvedDep` carries a `Resolution` field explaining how its version was (or wasn't) determined:

| value | meaning |
|---|---|
| `resolved` | concrete version produced |
| `unresolved_property` | a `${name}` survived interpolation and nothing defines it |
| `unresolved_env` | references `${env.X}`, never resolvable statically |
| `unresolved_parent` | a parent POM in the chain couldn't be fetched, so the result is suspect |
| `unresolved_profile_gated` | the property is only defined inside a profile that wasn't activated |
| `unresolved_missing` | no version anywhere reachable: not on the dep, not in dependencyManagement, not in any BOM |

When a tag is unresolved, `Expression` holds the original `${...}` string so callers can report it.

## Profiles

`Options.Profiles` controls which `<profile>` sections contribute. `OnlyDefault` (the default) activates only `<activeByDefault>true</activeByDefault>`. `Pessimistic` activates everything, on the basis that for vuln scanning a false positive beats a false negative. `Explicit` takes a list of IDs.

```go
pom.Options{Profiles: pom.ProfileActivation{Mode: pom.Pessimistic}}
```

Dependencies contributed by a profile carry `Profile` set to the profile ID so callers can attribute findings.

## Testing against real Maven

`testdata/poms/` holds 72 real POMs fetched from Maven Central (roots, parents, and BOMs) covering 31 artifacts: jackson, spring, junit, guava, log4j, netty, okhttp, kotlin-stdlib, hibernate, kafka, grpc, protobuf, micrometer, reactor, testcontainers, postgresql, logback, plus the reflections and modelmapper artifacts that triggered scrutineer#46. `testdata/expected/` holds the dependency lists that `mvn help:effective-pom` produced for each root. `TestGoldenAgainstMaven` resolves every root offline through `DirFetcher` and diffs 262 dependencies against the expected output.

To regenerate or extend the corpus, edit the `corpus` slice in `tools/refresh/main.go` and run:

```
go run ./tools/refresh
```

This needs network access and `mvn` on PATH.

One known divergence: dependencies whose identity is OS-gated (netty's `${os.detected.classifier}`, set by the os-maven-plugin extension and overridden by per-OS profiles) can't be resolved statically. Maven's own output for those varies by host. The golden test logs and tolerates them rather than failing.

## What this doesn't do

Plugin merging, lifecycle binding, `<build>` configuration, repository declarations, `settings.xml`, mirror selection, version-range mediation, transitive resolution. This is a model builder, not a dependency resolver. If you need a full tree, feed the output of this into something that walks transitive edges.

## License

MIT, see [LICENSE](LICENSE).
