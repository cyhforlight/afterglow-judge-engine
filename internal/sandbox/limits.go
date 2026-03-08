// Package sandbox provides resource limit constants for compilation and execution.
package sandbox

// Resource limit constants for execution.
const (
	// WallTimeMultiplier is the multiplier applied to CPU time limit to get wall time limit.
	// Wall time accounts for I/O waits, scheduling latency, container overhead, etc.
	// A multiplier of 3 means wall time can be up to 3x the CPU time limit.
	WallTimeMultiplier = 3

	// DefaultExecutionOutputLimitBytes is the maximum output size for user program execution.
	// Set to 16MB to prevent memory exhaustion from unbounded output.
	DefaultExecutionOutputLimitBytes = 16 * 1024 * 1024 // 16MB

	// DefaultCompileOutputLimitBytes is the maximum output size for compilation.
	// Set to 1MB as compile logs are typically small.
	DefaultCompileOutputLimitBytes = 1 * 1024 * 1024 // 1MB
)
