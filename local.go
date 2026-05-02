package pom

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// LocalFetcher resolves parent POMs from the filesystem by following
// <parent><relativePath> (defaulting to ../pom.xml) from a root file. It
// never touches the network; any GAV not found on disk returns an error,
// which the resolver records as a warning and tags as unresolved_parent.
type LocalFetcher struct {
	index map[GAV]*POM
}

// ResolveLocal is a convenience for the common case of resolving a pom.xml
// found in a source checkout: it reads path, walks the relativePath chain
// on disk, and computes the effective POM with no network access.
func ResolveLocal(ctx context.Context, path string, opts Options) (*EffectivePOM, error) {
	f, root, err := NewLocalFetcher(path)
	if err != nil {
		return nil, err
	}
	return NewResolver(f).ResolvePOM(ctx, root, opts)
}

// NewLocalFetcher reads the POM at path and every reachable parent via
// <relativePath>, indexing each by GAV. It returns the fetcher and the
// parsed root so callers can pass it straight to Resolver.ResolvePOM.
func NewLocalFetcher(path string) (*LocalFetcher, *POM, error) {
	root, err := readPOMFile(path)
	if err != nil {
		return nil, nil, err
	}
	return NewLocalFetcherFrom(root, filepath.Dir(path)), root, nil
}

// NewLocalFetcherFrom builds a LocalFetcher around an already-parsed root
// POM whose file lived in dir. Use this when the caller has the bytes in
// hand and wants to avoid re-reading the root.
func NewLocalFetcherFrom(root *POM, dir string) *LocalFetcher {
	f := &LocalFetcher{index: map[GAV]*POM{root.EffectiveGAV(): root}}
	f.walk(root, dir)
	return f
}

func (f *LocalFetcher) walk(p *POM, dir string) {
	for range maxParentDepth {
		if p.Parent == nil {
			return
		}
		rel := p.Parent.LocalPath()
		if rel == "" || filepath.IsAbs(rel) {
			return
		}
		path := filepath.Clean(filepath.Join(dir, rel))
		fi, err := os.Lstat(path)
		if err != nil {
			return
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return
		}
		if fi.IsDir() {
			path = filepath.Join(path, "pom.xml")
			fi, err = os.Lstat(path)
			if err != nil || fi.Mode()&os.ModeSymlink != 0 {
				return
			}
		}
		parent, err := readPOMFile(path)
		if err != nil {
			return
		}
		gav := parent.EffectiveGAV()
		if _, seen := f.index[gav]; seen {
			return
		}
		// Index under both the file's own GAV and the GAV the child
		// declared, since source trees commonly drift (child says
		// 1.0-SNAPSHOT, parent file says 1.0).
		f.index[gav] = parent
		f.index[p.Parent.GAV()] = parent
		p = parent
		dir = filepath.Dir(path)
	}
}

func (f *LocalFetcher) Fetch(_ context.Context, gav GAV) (*POM, error) {
	if p, ok := f.index[gav]; ok {
		return p, nil
	}
	return nil, fmt.Errorf("not found locally: %s", gav)
}

func readPOMFile(path string) (*POM, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParsePOM(data)
}
