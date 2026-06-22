package sandbox

import (
	"context"
	"math"

	cgroupsv2 "github.com/containerd/cgroups/v3/cgroup2/stats"
	"github.com/containerd/containerd/api/types"
	typeurl "github.com/containerd/typeurl/v2"
)

const nanosPerMs = uint64(1_000_000)

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

func collectMetrics(ctx context.Context, task metricsReader) cgroupMetrics {
	metric, err := task.Metrics(ctx)
	if err != nil {
		return cgroupMetrics{}
	}
	return parseCgroupMetrics(metric.Data)
}

func parseCgroupMetrics(data typeurl.Any) cgroupMetrics {
	var m cgroupMetrics

	var v2 cgroupsv2.Metrics
	if err := typeurl.UnmarshalTo(data, &v2); err != nil {
		return m
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
	return m
}
