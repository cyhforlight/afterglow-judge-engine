// Package workspace manages temporary directories for compilation and execution.
package workspace

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// File describes a file to be stored in a workspace.
type File struct {
	Name    string
	Content []byte
	Mode    os.FileMode
}

// Workspace manages a temporary directory for compilation or execution.
type Workspace struct {
	root *os.Root
}

// New creates a new temporary workspace directory.
func New() (*Workspace, error) {
	dir, err := os.MkdirTemp("", "sandbox-workspace-*")
	if err != nil {
		return nil, fmt.Errorf("create workspace: %w", err)
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open workspace root: %w", errors.Join(err, os.RemoveAll(dir)))
	}
	return &Workspace{root: root}, nil
}

// Dir returns the workspace directory path.
func (w *Workspace) Dir() string {
	return w.root.Name()
}

// WriteFiles writes multiple files to the workspace.
func (w *Workspace) WriteFiles(files []File) error {
	for _, file := range files {
		if strings.TrimSpace(file.Name) == "" {
			return errors.New("workspace path is required")
		}
		fileMode := file.Mode
		if fileMode == 0 {
			fileMode = 0o644
		}
		if err := w.root.WriteFile(file.Name, file.Content, fileMode); err != nil {
			return fmt.Errorf("write file %q: %w", file.Name, err)
		}
	}
	return nil
}

// ReadFile reads a file from the workspace.
func (w *Workspace) ReadFile(name string) ([]byte, error) {
	data, err := w.root.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("read file %q: %w", name, err)
	}
	return data, nil
}

// Stat returns file info for a file in the workspace.
func (w *Workspace) Stat(name string) (os.FileInfo, error) {
	info, err := w.root.Stat(name)
	if err != nil {
		return nil, fmt.Errorf("stat file %q: %w", name, err)
	}
	return info, nil
}

// Cleanup removes the workspace directory and all its contents.
func (w *Workspace) Cleanup() error {
	dir := w.root.Name()
	if err := errors.Join(w.root.Close(), os.RemoveAll(dir)); err != nil {
		return fmt.Errorf("cleanup workspace: %w", err)
	}
	return nil
}
