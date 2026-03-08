package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// InternalStorage implements read-only storage for project-bundled resources.
// Used for resources like testlib.h, ncmp, rcmp that ship with the project.
type InternalStorage struct {
	files map[string][]byte
}

const bundledSupportDirName = "support"

// NewInternalStorage creates a read-only, in-memory snapshot for internal resources.
func NewInternalStorage(baseDir string) (*InternalStorage, error) {
	files, err := loadSnapshot(baseDir)
	if err != nil {
		return nil, err
	}

	return &InternalStorage{files: files}, nil
}

// NewBundledInternalStorage creates a snapshot of the support directory next to the executable.
func NewBundledInternalStorage() (*InternalStorage, error) {
	executablePath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable path: %w", err)
	}

	supportDir, err := supportDirFromExecutable(executablePath)
	if err != nil {
		return nil, err
	}

	return NewInternalStorage(supportDir)
}

// Get retrieves resource content by key (key = relative path like "checkers/ncmp").
func (s *InternalStorage) Get(_ context.Context, key string) ([]byte, error) {
	normalizedKey, err := normalizeResourceKey(key)
	if err != nil {
		return nil, err
	}

	data, ok := s.files[normalizedKey]
	if !ok {
		return nil, fmt.Errorf("resource not found: %s", normalizedKey)
	}

	return bytes.Clone(data), nil
}

// Store is not supported (read-only).
func (s *InternalStorage) Store(_ context.Context, _ string, _ []byte) (string, error) {
	return "", errors.New("InternalStorage is read-only")
}

// StoreWithKey is not supported (read-only).
func (s *InternalStorage) StoreWithKey(_ context.Context, _ string, _ []byte) error {
	return errors.New("InternalStorage is read-only")
}

// Delete is not supported (read-only).
func (s *InternalStorage) Delete(_ context.Context, _ string) error {
	return errors.New("InternalStorage is read-only")
}

func loadSnapshot(baseDir string) (map[string][]byte, error) {
	info, err := os.Stat(baseDir)
	if err != nil {
		return nil, fmt.Errorf("base directory not accessible: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("base directory is not a directory: %q", baseDir)
	}

	files := make(map[string][]byte)
	err = filepath.WalkDir(baseDir, func(filePath string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk internal resource %q: %w", filePath, walkErr)
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("internal resource must be a regular file: %q", filePath)
		}

		relativePath, err := filepath.Rel(baseDir, filePath)
		if err != nil {
			return fmt.Errorf("resolve relative path for %q: %w", filePath, err)
		}

		key, err := normalizeResourceKey(relativePath)
		if err != nil {
			return fmt.Errorf("normalize internal resource key for %q: %w", filePath, err)
		}

		content, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("read internal resource %q: %w", filePath, err)
		}

		files[key] = content
		return nil
	})
	if err != nil {
		return nil, err
	}

	return files, nil
}

func supportDirFromExecutable(executablePath string) (string, error) {
	if executablePath == "" {
		return "", errors.New("executable path is required")
	}

	resolvedPath, err := filepath.EvalSymlinks(executablePath)
	if err != nil {
		return "", fmt.Errorf("resolve executable symlinks: %w", err)
	}

	return filepath.Join(filepath.Dir(resolvedPath), bundledSupportDirName), nil
}

func normalizeResourceKey(key string) (string, error) {
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
