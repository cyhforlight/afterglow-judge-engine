package resource

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
)

// External provides read-only access to external files.
type External struct {
	mountPoint string
}

// NewExternal creates a read-only resource store mounted at the specified directory.
func NewExternal(mountPoint string) (*External, error) {
	// Verify mount point exists and is a directory
	info, err := os.Stat(mountPoint)
	if err != nil {
		return nil, fmt.Errorf("mount point not accessible: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("mount point is not a directory: %q", mountPoint)
	}

	return &External{
		mountPoint: mountPoint,
	}, nil
}

// Get retrieves file content by relative path.
// The path is relative to the mount point (e.g., "testdata/input.txt").
func (e *External) Get(_ context.Context, relPath string) ([]byte, error) {
	file, err := e.openRegularFile(relPath)
	if err != nil {
		return nil, err
	}

	// Let the operating system page cache handle repeated reads.
	data, readErr := io.ReadAll(file)
	if err := errors.Join(readErr, file.Close()); err != nil {
		return nil, fmt.Errorf("read external resource %q: %w", relPath, err)
	}

	return data, nil
}

// Stat verifies that a relative path resolves to an accessible regular file inside the mount.
func (e *External) Stat(_ context.Context, relPath string) error {
	file, err := e.openRegularFile(relPath)
	if err != nil {
		return err
	}
	return file.Close()
}

func (e *External) openRegularFile(relPath string) (*os.File, error) {
	file, err := os.OpenInRoot(e.mountPoint, relPath)
	if err != nil {
		return nil, fmt.Errorf("open external resource %q: %w", relPath, err)
	}

	fileInfo, err := file.Stat()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("stat external resource %q: %w", relPath, err), file.Close())
	}
	if !fileInfo.Mode().IsRegular() {
		return nil, errors.Join(fmt.Errorf("external resource must be a regular file: %s", relPath), file.Close())
	}

	return file, nil
}
