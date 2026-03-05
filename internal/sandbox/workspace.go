package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
)

// Workspace manages a temporary directory for compilation or execution.
type Workspace struct {
	dir string
}

// NewWorkspace creates a new temporary workspace directory.
func NewWorkspace() (*Workspace, error) {
	dir, err := os.MkdirTemp("", "sandbox-workspace-*")
	if err != nil {
		return nil, fmt.Errorf("create workspace: %w", err)
	}
	return &Workspace{dir: dir}, nil
}

// Dir returns the workspace directory path.
func (w *Workspace) Dir() string {
	return w.dir
}

// WriteFile writes a file to the workspace with the given name, content, and permissions.
func (w *Workspace) WriteFile(name string, content []byte, mode os.FileMode) error {
	path := filepath.Join(w.dir, name)
	if err := os.WriteFile(path, content, mode); err != nil {
		return fmt.Errorf("write file %q: %w", name, err)
	}
	return nil
}

// ReadFile reads a file from the workspace.
func (w *Workspace) ReadFile(name string) ([]byte, error) {
	path := filepath.Join(w.dir, name)
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file %q: %w", name, err)
	}
	return content, nil
}

// Cleanup removes the workspace directory and all its contents.
func (w *Workspace) Cleanup() error {
	if err := os.RemoveAll(w.dir); err != nil {
		return fmt.Errorf("cleanup workspace: %w", err)
	}
	return nil
}
