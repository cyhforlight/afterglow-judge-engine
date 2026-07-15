package model

const (
	defaultMaxTimeLimitMs = 10_000
	defaultMaxMemoryMB    = 1024
	defaultMaxTestCases   = 64
	defaultMaxSourceBytes = 256 * 1024
)

// JudgeLimits defines request-level policy limits for one synchronous judge request.
type JudgeLimits struct {
	MaxTimeLimitMs int
	MaxMemoryMB    int
	MaxTestCases   int
	MaxSourceBytes int
}

// DefaultJudgeLimits returns conservative defaults for a single-node synchronous judge engine.
func DefaultJudgeLimits() JudgeLimits {
	return JudgeLimits{
		MaxTimeLimitMs: defaultMaxTimeLimitMs,
		MaxMemoryMB:    defaultMaxMemoryMB,
		MaxTestCases:   defaultMaxTestCases,
		MaxSourceBytes: defaultMaxSourceBytes,
	}
}
