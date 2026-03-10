package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"afterglow-judge-sandbox/internal/cache"
)

// ExternalStorage provides read-only access to external files with optional caching.
// Designed for user-provided test data that rarely changes.
type ExternalStorage struct {
	mountPoint string
	cache      *cache.Cache // Optional, nil disables caching
}

// NewExternalStorage creates a read-only storage mounted at the specified directory.
// If cache is nil, caching is disabled (graceful degradation).
func NewExternalStorage(mountPoint string, cache *cache.Cache) (*ExternalStorage, error) {
	// Verify mount point exists and is a directory
	info, err := os.Stat(mountPoint)
	if err != nil {
		return nil, fmt.Errorf("mount point not accessible: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("mount point is not a directory: %q", mountPoint)
	}

	return &ExternalStorage{
		mountPoint: mountPoint,
		cache:      cache,
	}, nil
}

// Get retrieves file content by relative path.
// The path is relative to the mount point (e.g., "testdata/input.txt").
func (s *ExternalStorage) Get(_ context.Context, relPath string) ([]byte, error) {
	// Normalize and validate path (prevent path traversal)
	normalized, err := NormalizeResourceKey(relPath)
	if err != nil {
		return nil, err
	}

	// Build full path
	fullPath := filepath.Join(s.mountPoint, normalized)

	// Resolve symlinks to prevent mount escape
	resolvedPath, err := filepath.EvalSymlinks(fullPath)
	if err != nil {
		return nil, fmt.Errorf("resolve symlink: %w", err)
	}

	// Resolve mount point symlinks
	resolvedMount, err := filepath.EvalSymlinks(s.mountPoint)
	if err != nil {
		return nil, fmt.Errorf("resolve mount point: %w", err)
	}

	// Verify resolved path is still within mount point
	relResolved, err := filepath.Rel(resolvedMount, resolvedPath)
	if err != nil || relResolved == ".." || strings.HasPrefix(relResolved, "../") {
		return nil, fmt.Errorf("symlink escapes mount point: %s", relPath)
	}

	// Get file info (use resolved path)
	fileInfo, err := os.Stat(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("file not found: %w", err)
	}

	// Try cache if enabled
	if s.cache != nil {
		// Cache key includes mount point to avoid collisions between instances
		cacheKey := generateCacheKey(s.mountPoint, normalized, fileInfo.ModTime().Unix())

		if data, ok := s.cache.Get(cacheKey); ok {
			return data, nil
		}
	}

	// Cache miss or disabled, read file (use resolved path)
	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	// Store in cache if enabled
	if s.cache != nil {
		cacheKey := generateCacheKey(s.mountPoint, normalized, fileInfo.ModTime().Unix())
		s.cache.Set(cacheKey, data)
	}

	return data, nil
}

// Store is not supported (read-only).
func (s *ExternalStorage) Store(_ context.Context, _ string, _ []byte) (string, error) {
	return "", errors.New("ExternalStorage is read-only")
}

// StoreWithKey is not supported (read-only).
func (s *ExternalStorage) StoreWithKey(_ context.Context, _ string, _ []byte) error {
	return errors.New("ExternalStorage is read-only")
}

// Delete is not supported (read-only).
func (s *ExternalStorage) Delete(_ context.Context, _ string) error {
	return errors.New("ExternalStorage is read-only")
}

// generateCacheKey creates a deterministic cache key from mount point, filepath and mtime.
// Including mount point prevents cache collisions when multiple ExternalStorage instances share a cache.
func generateCacheKey(mountPoint, relPath string, mtimeUnix int64) string {
	h := sha256.New()
	h.Write([]byte(mountPoint))
	h.Write([]byte("|"))
	h.Write([]byte(relPath))
	h.Write([]byte("|"))
	h.Write([]byte(strconv.FormatInt(mtimeUnix, 10)))
	return hex.EncodeToString(h.Sum(nil))
}
