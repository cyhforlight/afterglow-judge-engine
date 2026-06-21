package sandbox

import (
	"fmt"
	"time"
)

const memoryHitThresholdPermille = 995

func buildVerdict(
	exitCode uint32,
	wallElapsed time.Duration,
	metrics cgroupMetrics,
	limits ResourceLimits,
	stdoutLW, stderrLW *limitedWriter,
) ExecuteResult {
	cpuMs := metrics.cpuMillis()
	peakMemMB := metrics.peakMemMB()

	res := ExecuteResult{
		ExitCode:  int(exitCode),
		Stdout:    stdoutLW.String(),
		Stderr:    stderrLW.String(),
		CPUTimeMs: cpuMs,
		MemoryMB:  peakMemMB,
	}

	if cpuMs == 0 {
		res.CPUTimeMs = int(wallElapsed.Milliseconds())
	}

	memoryHitLimit := memoryLimitReached(metrics, limits.MemoryMB)

	switch {
	case outputOverflowed(stdoutLW, stderrLW):
		res.Verdict = VerdictOLE
		res.ExtraInfo = fmt.Sprintf("output limit exceeded (%d bytes max)", limits.OutputBytes)

	case metrics.oomKillDetected || exitCode == 137 || (exitCode != 0 && memoryHitLimit):
		res.Verdict = VerdictMLE
		res.ExtraInfo = fmt.Sprintf("memory limit exceeded (peak %dMB, limit %dMB)", peakMemMB, limits.MemoryMB)

	case cpuMs > limits.CPUTimeMs:
		res.Verdict = VerdictTLE
		res.ExtraInfo = fmt.Sprintf("CPU time exceeded: %dms > %dms", cpuMs, limits.CPUTimeMs)

	case exitCode == 0:
		res.Verdict = VerdictOK

	default:
		res.Verdict = VerdictRE
		res.ExtraInfo = stderrLW.String()
	}
	return res
}

func buildForcedStopVerdict(
	reason string,
	metrics cgroupMetrics,
	limits ResourceLimits,
	stdoutLW, stderrLW *limitedWriter,
) ExecuteResult {
	cpuMs := min(metrics.cpuMillis(), limits.CPUTimeMs)
	peakMemMB := metrics.peakMemMB()

	res := ExecuteResult{
		CPUTimeMs: cpuMs,
		MemoryMB:  peakMemMB,
		Stdout:    stdoutLW.String(),
		Stderr:    stderrLW.String(),
	}

	if outputOverflowed(stdoutLW, stderrLW) {
		res.Verdict = VerdictOLE
		res.ExtraInfo = fmt.Sprintf("output limit exceeded (%d bytes max)", limits.OutputBytes)
		return res
	}
	if metrics.oomKillDetected || memoryLimitReached(metrics, limits.MemoryMB) {
		res.Verdict = VerdictMLE
		res.ExtraInfo = fmt.Sprintf("memory limit exceeded (peak %dMB, limit %dMB)", peakMemMB, limits.MemoryMB)
		return res
	}
	res.Verdict = VerdictTLE
	res.ExtraInfo = fmt.Sprintf("%s (%dms wall, cpu limit %dms)", reason, limits.WallTimeMs, limits.CPUTimeMs)
	return res
}

func memoryLimitReached(metrics cgroupMetrics, memoryLimitMB int) bool {
	if metrics.memoryLimitHit {
		return true
	}
	if memoryLimitMB <= 0 {
		return false
	}

	limitBytes := uint64(memoryLimitMB) * uint64(bytesPerMiB)
	if metrics.peakMemBytes >= limitBytes {
		return true
	}
	return metrics.peakMemBytes*1000 >= limitBytes*memoryHitThresholdPermille
}

func outputOverflowed(stdoutLW, stderrLW *limitedWriter) bool {
	return stdoutLW.isOverflowed() || stderrLW.isOverflowed()
}
