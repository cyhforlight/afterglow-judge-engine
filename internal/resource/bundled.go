// Package resource provides read-only access to bundled and external judge resources.
package resource

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	iofs "io/fs"

	rootassets "afterglow-judge-engine"
)

// Bundled implements read-only access to project-bundled resources.
// Used for resources like testlib.h and builtin checker sources.
type Bundled struct {
	fsys iofs.FS
}

const bundledSupportDirName = "support"

// NewBundled creates a resource store backed by embedded support resources.
func NewBundled() (*Bundled, error) {
	bundledFS, err := iofs.Sub(rootassets.BundledSupportFiles, bundledSupportDirName)
	if err != nil {
		return nil, fmt.Errorf("open bundled support resources: %w", err)
	}

	return newBundled(bundledFS), nil
}

func newBundled(fsys iofs.FS) *Bundled {
	return &Bundled{fsys: fsys}
}

// Get retrieves bundled resource content by trusted relative key.
func (b *Bundled) Get(_ context.Context, key string) ([]byte, error) {
	data, err := iofs.ReadFile(b.fsys, key)
	if errors.Is(err, iofs.ErrNotExist) {
		return nil, fmt.Errorf("resource not found: %s", key)
	}
	if err != nil {
		return nil, fmt.Errorf("read bundled resource %q: %w", key, err)
	}

	return bytes.Clone(data), nil
}

// Stat verifies that a bundled resource key exists.
func (b *Bundled) Stat(_ context.Context, key string) error {
	if _, err := iofs.Stat(b.fsys, key); errors.Is(err, iofs.ErrNotExist) {
		return fmt.Errorf("resource not found: %s", key)
	} else if err != nil {
		return fmt.Errorf("stat bundled resource %q: %w", key, err)
	}

	return nil
}
