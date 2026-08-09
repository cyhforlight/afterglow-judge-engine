package sandbox

import (
	"context"
	"testing"
	"testing/synctest"

	cgroupsv2 "github.com/containerd/cgroups/v3/cgroup2/stats"
	"github.com/containerd/containerd/api/types"
	typeurl "github.com/containerd/typeurl/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type blockingMetricsReader struct{}

func (blockingMetricsReader) Metrics(ctx context.Context) (*types.Metric, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

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
			Oom:     1,
			OomKill: 1,
		},
	})
	require.NoError(t, err)

	got, err := parseCgroupMetrics(raw)
	require.NoError(t, err)

	assert.Equal(t, uint64(12_345_000), got.cpuNanos)
	assert.Equal(t, uint64(48*bytesPerMiB), got.peakMemBytes)
	assert.True(t, got.oomDetected)
	assert.True(t, got.oomKillDetected)
}

func TestCollectMetrics_TimesOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		_, err := collectMetrics(t.Context(), blockingMetricsReader{})

		require.ErrorIs(t, err, context.DeadlineExceeded)
	})
}
