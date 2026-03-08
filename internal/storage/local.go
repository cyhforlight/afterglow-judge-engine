package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LocalStorage implements Storage using the local filesystem.
type LocalStorage struct {
	baseDir string
}

// NewLocalStorage creates a LocalStorage instance.
// If baseDir is empty, uses os.TempDir().
func NewLocalStorage(baseDir string) (*LocalStorage, error) {
	if baseDir == "" {
		baseDir = os.TempDir()
	}

	// Ensure base directory exists
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create base directory: %w", err)
	}

	return &LocalStorage{baseDir: baseDir}, nil
}

// Store saves content to a temporary file and returns its key.
func (s *LocalStorage) Store(_ context.Context, name string, content []byte) (string, error) {
	// Generate unique key
	key, err := generateKey()
	if err != nil {
		return "", fmt.Errorf("failed to generate key: %w", err)
	}

	// Sanitize filename
	safeName := sanitizeFilename(name)
	if safeName == "" {
		safeName = "file"
	}

	// Create file path: baseDir/key_safeName
	filePath := filepath.Join(s.baseDir, fmt.Sprintf("%s_%s", key, safeName))

	// Write content
	if err := os.WriteFile(filePath, content, 0o644); err != nil {
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	return key, nil
}

// StoreWithKey saves content with a specific key (deterministic).
func (s *LocalStorage) StoreWithKey(_ context.Context, key string, content []byte) error {
	if err := validateKey(key); err != nil {
		return err
	}

	filePath := filepath.Join(s.baseDir, key)

	if err := os.WriteFile(filePath, content, 0o644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

// Get retrieves content by key.
// Handles both Store() files (key_filename) and StoreWithKey() files (just key).
func (s *LocalStorage) Get(_ context.Context, key string) ([]byte, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}

	// First try exact key match (from StoreWithKey)
	exactPath := filepath.Join(s.baseDir, key)
	if data, err := os.ReadFile(exactPath); err == nil {
		return data, nil
	}

	// Then try key_* pattern (from Store)
	pattern := filepath.Join(s.baseDir, key+"_*")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to search for file: %w", err)
	}

	if len(matches) == 0 {
		return nil, fmt.Errorf("file not found: %s", key)
	}

	data, err := os.ReadFile(matches[0])
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	return data, nil
}

// Delete removes a file by key.
// Handles both Store() files (key_filename) and StoreWithKey() files (just key).
func (s *LocalStorage) Delete(_ context.Context, key string) error {
	if err := validateKey(key); err != nil {
		return err
	}

	// Try exact key match first (from StoreWithKey)
	exactPath := filepath.Join(s.baseDir, key)
	if err := os.Remove(exactPath); err == nil {
		return nil
	}

	// Then try key_* pattern (from Store)
	pattern := filepath.Join(s.baseDir, key+"_*")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("failed to search for file: %w", err)
	}

	if len(matches) == 0 {
		return fmt.Errorf("file not found: %s", key)
	}

	for _, path := range matches {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("failed to delete file: %w", err)
		}
	}

	return nil
}

// generateKey creates a random hex key.
func generateKey() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// sanitizeFilename removes dangerous characters from filenames.
func sanitizeFilename(name string) string {
	// Remove path separators and dangerous characters
	name = filepath.Base(name)
	name = strings.ReplaceAll(name, "..", "")
	name = strings.TrimSpace(name)

	// Only allow alphanumeric, dash, underscore, and dot
	var safe strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			safe.WriteRune(r)
		}
	}

	result := safe.String()
	if len(result) > 255 {
		result = result[:255]
	}

	return result
}

// validateKey checks if a key has valid format.
func validateKey(key string) error {
	if key == "" {
		return errors.New("key cannot be empty")
	}

	// Key should be hex string
	if _, err := hex.DecodeString(key); err != nil {
		return fmt.Errorf("invalid key format: %w", err)
	}

	// Check for path traversal
	if strings.Contains(key, "..") || strings.Contains(key, "/") || strings.Contains(key, "\\") {
		return errors.New("key contains invalid characters")
	}

	return nil
}
