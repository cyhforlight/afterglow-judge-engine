package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// InternalStorage implements read-only storage for project-bundled resources.
// Used for resources like testlib.h, ncmp, rcmp that ship with the project.
type InternalStorage struct {
	baseDir string
}

// NewInternalStorage creates a read-only storage for internal resources.
func NewInternalStorage(baseDir string) (*InternalStorage, error) {
	if _, err := os.Stat(baseDir); err != nil {
		return nil, fmt.Errorf("base directory not accessible: %w", err)
	}
	return &InternalStorage{baseDir: baseDir}, nil
}

// Get retrieves resource content by key (key = relative path like "checkers/ncmp").
func (s *InternalStorage) Get(_ context.Context, key string) ([]byte, error) {
	filePath := filepath.Join(s.baseDir, key)

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("resource not found: %w", err)
	}

	return data, nil
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
