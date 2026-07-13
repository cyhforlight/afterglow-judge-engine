package resource

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// External provides read-only access to external files.
type External struct {
	mountPoint string
}

// NewExternal creates a read-only file system rooted at the specified directory.
func NewExternal(mountPoint string) (*External, error) {
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

// Open opens a regular file relative to the external resource root.
func (e *External) Open(relPath string) (fs.File, error) {
	if !fs.ValidPath(relPath) {
		return nil, &fs.PathError{Op: "open", Path: relPath, Err: fs.ErrInvalid}
	}

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
