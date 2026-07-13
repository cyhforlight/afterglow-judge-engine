package sandbox

import (
	"context"
	"errors"
	"math"
	"testing"

	cgroupsv2 "github.com/containerd/cgroups/v3/cgroup2/stats"
	"github.com/containerd/containerd/api/types"
	typeurl "github.com/containerd/typeurl/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"
)

type fakeMetricsReader struct {
	metric *types.Metric
	err    error
}

func (r fakeMetricsReader) Metrics(context.Context) (*types.Metric, error) {
	return r.metric, r.err
}

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
			Max:     1,
			OomKill: 1,
		},
	})
	require.NoError(t, err)

	got, err := parseCgroupMetrics(raw)
	require.NoError(t, err)

	assert.Equal(t, uint64(12_345_000), got.cpuNanos)
	assert.Equal(t, uint64(48*bytesPerMiB), got.peakMemBytes)
	assert.True(t, got.memoryLimitHit)
	assert.True(t, got.oomKillDetected)
}

func TestCollectMetrics_ReturnsReadErrors(t *testing.T) {
	readErr := errors.New("metrics unavailable")
	tests := []struct {
		name    string
		reader  fakeMetricsReader
		wantErr string
	}{
		{
			name:    "containerd read failure",
			reader:  fakeMetricsReader{err: readErr},
			wantErr: "read task metrics: metrics unavailable",
		},
		{
			name:    "nil response",
			reader:  fakeMetricsReader{},
			wantErr: "response contains no data",
		},
		{
			name:    "missing metric data",
			reader:  fakeMetricsReader{metric: &types.Metric{}},
			wantErr: "response contains no data",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := collectMetrics(context.Background(), tt.reader)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestParseCgroupMetrics_ReturnsMalformedDataError(t *testing.T) {
	raw, err := typeurl.MarshalAny(&cgroupsv2.Metrics{})
	require.NoError(t, err)

	_, err = parseCgroupMetrics(&anypb.Any{
		TypeUrl: raw.GetTypeUrl(),
		Value:   []byte{0xff},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal cgroup v2 metrics")
}

func TestCollectMetrics_TimesOut(t *testing.T) {
	_, err := collectMetrics(context.Background(), blockingMetricsReader{})

	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestUint64ToInt_SaturatesOverflow(t *testing.T) {
	assert.Equal(t, math.MaxInt, uint64ToInt(uint64(math.MaxInt)+1))
	assert.Equal(t, 42, uint64ToInt(42))
}
