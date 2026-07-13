// Package resource provides read-only access to bundled and external judge resources.
package resource

import (
	"fmt"
	"io/fs"

	rootassets "afterglow-judge-engine"
)

const bundledSupportDirName = "support"

// NewBundled creates a file system backed by embedded support resources.
func NewBundled() (fs.FS, error) {
	bundledFS, err := fs.Sub(rootassets.BundledSupportFiles, bundledSupportDirName)
	if err != nil {
		return nil, fmt.Errorf("open bundled support resources: %w", err)
	}

	return bundledFS, nil
}
