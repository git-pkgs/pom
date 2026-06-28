package pom

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// LocalFetcher resolves parent POMs from the filesystem by following
// <parent><relativePath> (defaulting to ../pom.xml) from a root file. It
// never touches the network; any GAV not found on disk returns an error,
// which the resolver records as a warning and tags as unresolved_parent.
//
// The walk is jailed to the directory passed at construction time. An empty
// jail root disables the walk entirely so the fetcher only knows about the
// already-parsed root POM. This is the safe choice when the POM bytes came
// from an untrusted source and there is no on-disk checkout to consult.
type LocalFetcher struct {
	root  string
	index map[GAV]*POM
}

// ResolveLocal is a convenience for the common case of resolving a pom.xml
// found in a source checkout: it reads path, walks the relativePath chain
// on disk within fsRoot, and computes the effective POM with no network
// access.
func ResolveLocal(ctx context.Context, path, fsRoot string, opts Options) (*EffectivePOM, error) {
	f, root, err := NewLocalFetcher(path, fsRoot)
	if err != nil {
		return nil, err
	}
	return NewResolver(f).ResolvePOM(ctx, root, opts)
}

// NewLocalFetcher reads the POM at path and every reachable parent via
// <relativePath> within fsRoot, indexing each by GAV. It returns the
// fetcher and the parsed root so callers can pass it straight to
// Resolver.ResolvePOM.
func NewLocalFetcher(path, fsRoot string) (*LocalFetcher, *POM, error) {
	root, err := readPOMFile(path)
	if err != nil {
		return nil, nil, err
	}
	return NewLocalFetcherFrom(root, filepath.Dir(path), fsRoot), root, nil
}

// NewLocalFetcherFrom builds a LocalFetcher around an already-parsed root
// POM whose file lived in dir. The relativePath walk is confined to fsRoot;
// pass an empty fsRoot to skip the walk entirely.
func NewLocalFetcherFrom(root *POM, dir, fsRoot string) *LocalFetcher {
	f := &LocalFetcher{index: map[GAV]*POM{root.EffectiveGAV(): root}}
	if fsRoot != "" {
		if abs, err := filepath.Abs(fsRoot); err == nil {
			f.root = filepath.Clean(abs)
			f.walk(root, dir)
		}
	}
	return f
}

func (f *LocalFetcher) within(path string) bool {
	if f.root == "" {
		return false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	abs = filepath.Clean(abs)
	if abs == f.root {
		return true
	}
	return strings.HasPrefix(abs, f.root+string(filepath.Separator))
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
		if !f.within(path) {
			return
		}
		fi, err := os.Lstat(path)
		if err != nil {
			return
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return
		}
		if fi.IsDir() {
			path = filepath.Join(path, "pom.xml")
			if !f.within(path) {
				return
			}
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
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = fh.Close() }()
	data, err := io.ReadAll(io.LimitReader(fh, MaxPOMBytes+1))
	if err != nil {
		return nil, err
	}
	return ParsePOM(data)
}
