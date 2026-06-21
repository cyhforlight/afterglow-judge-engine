package sandbox

import (
	"math"
	"testing"

	cgroupsv2 "github.com/containerd/cgroups/v3/cgroup2/stats"
	typeurl "github.com/containerd/typeurl/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCgroupMetrics_MapsV2Stats(t *testing.T) {
	raw, err := typeurl.MarshalAny(&cgroupsv2.Metrics{
		CPU: &cgroupsv2.CPUStat{
			UsageUsec: 12_345,
		},
		Memory: &cgroupsv2.MemoryStat{
			Usage:    uint64(32 * bytesPerMiB),
			MaxUsage: uint64(48 * bytesPerMiB),
		},
		MemoryEvents: &cgroupsv2.MemoryEvents{
			Max:     1,
			OomKill: 1,
		},
	})
	require.NoError(t, err)

	got := parseCgroupMetrics(raw)

	assert.Equal(t, uint64(12_345_000), got.cpuNanos)
	assert.Equal(t, uint64(48*bytesPerMiB), got.peakMemBytes)
	assert.True(t, got.memoryLimitHit)
	assert.True(t, got.oomKillDetected)
}

func TestUint64ToInt_SaturatesOverflow(t *testing.T) {
	assert.Equal(t, math.MaxInt, uint64ToInt(uint64(math.MaxInt)+1))
	assert.Equal(t, 42, uint64ToInt(42))
}
