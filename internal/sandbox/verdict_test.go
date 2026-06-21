package sandbox

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildVerdict_OutputLimitHasHighestPriority(t *testing.T) {
	limits := ResourceLimits{
		CPUTimeMs:   100,
		WallTimeMs:  300,
		MemoryMB:    64,
		OutputBytes: 4,
	}
	stdout, stderr := verdictWriters(t, limits.OutputBytes, "hello")
	metrics := cgroupMetrics{
		cpuNanos:        500 * nanosPerMs,
		peakMemBytes:    uint64(128 * bytesPerMiB),
		memoryLimitHit:  true,
		oomKillDetected: true,
	}

	got := buildVerdict(137, time.Second, metrics, limits, stdout, stderr)

	assert.Equal(t, VerdictOLE, got.Verdict)
	assert.Contains(t, got.ExtraInfo, "output limit exceeded")
}

func TestBuildVerdict_MemoryThresholdHandlesCgroupUnderreporting(t *testing.T) {
	limits := ResourceLimits{
		CPUTimeMs:   1000,
		WallTimeMs:  3000,
		MemoryMB:    128,
		OutputBytes: 1024,
	}
	stdout, stderr := verdictWriters(t, limits.OutputBytes, "")
	nearLimitBytes := uint64(limits.MemoryMB)*uint64(bytesPerMiB) - 1

	got := buildVerdict(1, time.Second, cgroupMetrics{peakMemBytes: nearLimitBytes}, limits, stdout, stderr)

	assert.Equal(t, VerdictMLE, got.Verdict)
	assert.Contains(t, got.ExtraInfo, "memory limit exceeded")
}

func TestBuildVerdict_UsesWallTimeWhenCPUStatsAreMissing(t *testing.T) {
	limits := ResourceLimits{
		CPUTimeMs:   1000,
		WallTimeMs:  3000,
		MemoryMB:    128,
		OutputBytes: 1024,
	}
	stdout, stderr := verdictWriters(t, limits.OutputBytes, "")

	got := buildVerdict(0, 42*time.Millisecond, cgroupMetrics{}, limits, stdout, stderr)

	assert.Equal(t, VerdictOK, got.Verdict)
	assert.Equal(t, 42, got.CPUTimeMs)
}

func TestBuildForcedStopVerdict_PrioritizesConcreteResourceFailures(t *testing.T) {
	limits := ResourceLimits{
		CPUTimeMs:   100,
		WallTimeMs:  300,
		MemoryMB:    64,
		OutputBytes: 4,
	}

	tests := []struct {
		name       string
		stdoutText string
		metrics    cgroupMetrics
		want       Verdict
		wantInfo   string
		wantCPU    int
	}{
		{
			name:       "output overflow beats memory and timeout",
			stdoutText: "hello",
			metrics: cgroupMetrics{
				cpuNanos:        500 * nanosPerMs,
				memoryLimitHit:  true,
				oomKillDetected: true,
			},
			want:     VerdictOLE,
			wantInfo: "output limit exceeded",
			wantCPU:  100,
		},
		{
			name:     "memory event beats generic timeout",
			metrics:  cgroupMetrics{memoryLimitHit: true},
			want:     VerdictMLE,
			wantInfo: "memory limit exceeded",
			wantCPU:  0,
		},
		{
			name:     "timeout caps reported cpu at requested limit",
			metrics:  cgroupMetrics{cpuNanos: 500 * nanosPerMs},
			want:     VerdictTLE,
			wantInfo: "wall time limit exceeded",
			wantCPU:  100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr := verdictWriters(t, limits.OutputBytes, tt.stdoutText)

			got := buildForcedStopVerdict("wall time limit exceeded", tt.metrics, limits, stdout, stderr)

			assert.Equal(t, tt.want, got.Verdict)
			assert.Contains(t, got.ExtraInfo, tt.wantInfo)
			assert.Equal(t, tt.wantCPU, got.CPUTimeMs)
		})
	}
}

func verdictWriters(t *testing.T, outputLimit int64, stdoutText string) (*limitedWriter, *limitedWriter) {
	t.Helper()

	limiter := newOutputLimiter(outputLimit)
	stdout := newLimitedWriter(limiter)
	stderr := newLimitedWriter(limiter)

	if stdoutText != "" {
		_, err := stdout.Write([]byte(stdoutText))
		require.NoError(t, err)
	}
	return stdout, stderr
}
