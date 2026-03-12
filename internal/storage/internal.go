package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	iofs "io/fs"
	"path"
	"path/filepath"
	"strings"

	rootassets "afterglow-judge-engine"
)

// InternalStorage implements read-only storage for project-bundled resources.
// Used for resources like testlib.h and builtin checker sources.
type InternalStorage struct {
	fsys iofs.FS
}

const bundledSupportDirName = "support"

// NewBundledInternalStorage creates a storage backed by embedded support resources.
func NewBundledInternalStorage() (*InternalStorage, error) {
	bundledFS, err := iofs.Sub(rootassets.BundledSupportFiles, bundledSupportDirName)
	if err != nil {
		return nil, fmt.Errorf("open bundled support resources: %w", err)
	}

	return newInternalStorage(bundledFS), nil
}

func newInternalStorage(fsys iofs.FS) *InternalStorage {
	return &InternalStorage{fsys: fsys}
}

// Get retrieves resource content by key (key = relative path like "checkers/ncmp").
func (s *InternalStorage) Get(_ context.Context, key string) ([]byte, error) {
	normalizedKey, err := NormalizeResourceKey(key)
	if err != nil {
		return nil, err
	}

	data, err := iofs.ReadFile(s.fsys, normalizedKey)
	if errors.Is(err, iofs.ErrNotExist) {
		return nil, fmt.Errorf("resource not found: %s", normalizedKey)
	}
	if err != nil {
		return nil, fmt.Errorf("read internal resource %q: %w", normalizedKey, err)
	}

	return bytes.Clone(data), nil
}

// Stat verifies that a resource key exists in storage.
func (s *InternalStorage) Stat(_ context.Context, key string) error {
	normalizedKey, err := NormalizeResourceKey(key)
	if err != nil {
		return err
	}

	if _, err := iofs.Stat(s.fsys, normalizedKey); errors.Is(err, iofs.ErrNotExist) {
		return fmt.Errorf("resource not found: %s", normalizedKey)
	} else if err != nil {
		return fmt.Errorf("stat internal resource %q: %w", normalizedKey, err)
	}

	return nil
}

// NormalizeResourceKey validates and normalizes a resource key.
func NormalizeResourceKey(key string) (string, error) {
	if strings.TrimSpace(key) == "" {
		return "", errors.New("resource key is required")
	}
	if filepath.IsAbs(key) {
		return "", fmt.Errorf("resource key must be relative: %q", key)
	}

	normalizedKey := path.Clean(filepath.ToSlash(key))
	if normalizedKey == "." {
		return "", errors.New("resource key is required")
	}
	if normalizedKey == ".." || strings.HasPrefix(normalizedKey, "../") {
		return "", fmt.Errorf("resource key escapes base directory: %q", key)
	}

	return normalizedKey, nil
}
