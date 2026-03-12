package afterglowjudgeengine

import "embed"

// BundledSupportFiles contains project-bundled checker resources compiled into the binary.
//
//go:embed support/**
var BundledSupportFiles embed.FS
