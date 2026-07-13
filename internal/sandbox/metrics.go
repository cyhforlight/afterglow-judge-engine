package sandbox

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	cgroupsv2 "github.com/containerd/cgroups/v3/cgroup2/stats"
	"github.com/containerd/containerd/api/types"
	typeurl "github.com/containerd/typeurl/v2"
)

const (
	nanosPerMs         = uint64(1_000_000)
	metricsReadTimeout = 100 * time.Millisecond
)

type cgroupMetrics struct {
	cpuNanos        uint64
	peakMemBytes    uint64
	memoryLimitHit  bool
	oomKillDetected bool
}

type metricsReader interface {
	Metrics(context.Context) (*types.Metric, error)
}

func (m cgroupMetrics) cpuMillis() int {
	return uint64ToInt(m.cpuNanos / nanosPerMs)
}

func (m cgroupMetrics) peakMemMB() int {
	return uint64ToInt(m.peakMemBytes / uint64(bytesPerMiB))
}

func uint64ToInt(value uint64) int {
	if value > uint64(math.MaxInt) {
		return math.MaxInt
	}
	return int(value)
}

func collectMetrics(ctx context.Context, task metricsReader) (cgroupMetrics, error) {
	metricsCtx, cancel := context.WithTimeout(ctx, metricsReadTimeout)
	defer cancel()

	metric, err := task.Metrics(metricsCtx)
	if err != nil {
		return cgroupMetrics{}, fmt.Errorf("read task metrics: %w", err)
	}
	if metric == nil || metric.Data == nil {
		return cgroupMetrics{}, errors.New("read task metrics: response contains no data")
	}
	return parseCgroupMetrics(metric.Data)
}

func parseCgroupMetrics(data typeurl.Any) (cgroupMetrics, error) {
	var m cgroupMetrics

	var v2 cgroupsv2.Metrics
	if err := typeurl.UnmarshalTo(data, &v2); err != nil {
		return m, fmt.Errorf("unmarshal cgroup v2 metrics: %w", err)
	}

	if v2.CPU != nil {
		m.cpuNanos = v2.CPU.UsageUsec * 1000
	}
	if v2.Memory != nil {
		m.peakMemBytes = max(v2.Memory.MaxUsage, v2.Memory.Usage)
	}
	if v2.MemoryEvents != nil {
		if v2.MemoryEvents.OomKill > 0 {
			m.oomKillDetected = true
		}
		if v2.MemoryEvents.Max > 0 || v2.MemoryEvents.Oom > 0 {
			m.memoryLimitHit = true
		}
	}
	return m, nil
}
