// Package storage provides abstractions for file storage backends.
package storage

import (
	"context"
)

// Storage abstracts file storage operations.
// Implementations can use local filesystem, S3, MinIO, etc.
//
// Design principle: Storage returns content directly (not paths) for complete encapsulation.
// This eliminates cleanup complexity and race conditions with LRU eviction.
type Storage interface {
	// Store saves content and returns a storage key (random).
	// The name parameter is used as a hint for the filename.
	Store(ctx context.Context, name string, content []byte) (key string, err error)

	// StoreWithKey saves content with a specific key (deterministic).
	// Used for content-addressable storage like compilation caches.
	StoreWithKey(ctx context.Context, key string, content []byte) error

	// Get retrieves content by key.
	// Returns the file content directly (not a path).
	Get(ctx context.Context, key string) ([]byte, error)

	// Delete removes content by key.
	Delete(ctx context.Context, key string) error
}
