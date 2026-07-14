package execution

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

type workspace struct {
	root *os.Root
}

func newWorkspace() (*workspace, error) {
	dir, err := os.MkdirTemp("", "sandbox-workspace-*")
	if err != nil {
		return nil, fmt.Errorf("create workspace: %w", err)
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open workspace root: %w", errors.Join(err, os.RemoveAll(dir)))
	}
	return &workspace{root: root}, nil
}

func (w *workspace) dir() string {
	return w.root.Name()
}

func (w *workspace) writeFiles(files []File) error {
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

func (w *workspace) readFile(name string) ([]byte, error) {
	data, err := w.root.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("read file %q: %w", name, err)
	}
	return data, nil
}

func (w *workspace) stat(name string) (os.FileInfo, error) {
	info, err := w.root.Stat(name)
	if err != nil {
		return nil, fmt.Errorf("stat file %q: %w", name, err)
	}
	return info, nil
}

func (w *workspace) cleanup() error {
	dir := w.root.Name()
	if err := errors.Join(w.root.Close(), os.RemoveAll(dir)); err != nil {
		return fmt.Errorf("cleanup workspace: %w", err)
	}
	return nil
}
