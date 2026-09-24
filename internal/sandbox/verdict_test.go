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
			got := buildVerdict(executionOutcome{exitCode: tt.exitCode, metrics: tt.metrics}, executionOutput{}, limits)

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
	got := buildVerdict(executionOutcome{metrics: cgroupMetrics{cpuNanos: 100 * nanosPerMs}}, executionOutput{}, limits)

	assert.Equal(t, VerdictTLE, got.Verdict)
}

func TestBuildVerdict_PreservesForcedStopReason(t *testing.T) {
	for _, reason := range []stopReason{stopCPUTime, stopWallTime} {
		t.Run(reason.String(), func(t *testing.T) {
			outcome := executionOutcome{reason: reason, metrics: cgroupMetrics{oomKillDetected: true}}
			result := buildVerdict(outcome, executionOutput{overflowed: true}, standardLimits())
			assert.Equal(t, VerdictTLE, result.Verdict)
			assert.Contains(t, result.ExtraInfo, reason.String())
		})
	}
}
