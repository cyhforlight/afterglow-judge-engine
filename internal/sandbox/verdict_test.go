package sandbox

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildVerdict_UsesCgroupOOMEvents(t *testing.T) {
	limits := ResourceLimits{
		CPUTimeMs:   100,
		WallTimeMs:  300,
		MemoryMB:    128,
		OutputBytes: 1024,
	}
	tests := []struct {
		name     string
		exitCode uint32
		metrics  cgroupMetrics
	}{
		{
			name:     "OOM kill",
			exitCode: 0,
			metrics:  cgroupMetrics{oomKillDetected: true},
		},
		{
			name:     "failed after OOM",
			exitCode: 1,
			metrics: cgroupMetrics{
				cpuNanos:    100 * nanosPerMs,
				oomDetected: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr := verdictWriters(limits.OutputBytes)

			got := buildVerdict(tt.exitCode, tt.metrics, limits, stdout, stderr)

			assert.Equal(t, VerdictMLE, got.Verdict)
			assert.Contains(t, got.ExtraInfo, "memory limit exceeded")
		})
	}
}

func TestBuildVerdict_CPUTimeAtLimitIsTLE(t *testing.T) {
	limits := ResourceLimits{
		CPUTimeMs:   100,
		WallTimeMs:  300,
		MemoryMB:    128,
		OutputBytes: 1024,
	}
	stdout, stderr := verdictWriters(limits.OutputBytes)

	got := buildVerdict(0, cgroupMetrics{cpuNanos: 100 * nanosPerMs}, limits, stdout, stderr)

	assert.Equal(t, VerdictTLE, got.Verdict)
}

func verdictWriters(outputLimit int64) (*limitedWriter, *limitedWriter) {
	limiter := newOutputLimiter(outputLimit)
	return newLimitedWriter(limiter), newLimitedWriter(limiter)
}
