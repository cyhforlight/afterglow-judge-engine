package sandbox

import "fmt"

func buildVerdict(
	exitCode uint32,
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

	switch {
	case stdoutLW.isOverflowed() || stderrLW.isOverflowed():
		res.Verdict = VerdictOLE
		res.ExtraInfo = fmt.Sprintf("output limit exceeded (%d bytes max)", limits.OutputBytes)

	case metrics.oomKillDetected || (exitCode != 0 && metrics.oomDetected):
		res.Verdict = VerdictMLE
		res.ExtraInfo = fmt.Sprintf("memory limit exceeded (peak %dMB, limit %dMB)", peakMemMB, limits.MemoryMB)

	case cpuMs >= limits.CPUTimeMs:
		res.Verdict = VerdictTLE
		res.ExtraInfo = fmt.Sprintf("CPU time exceeded: %dms >= %dms", cpuMs, limits.CPUTimeMs)

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
	cpuMs := metrics.cpuMillis()
	peakMemMB := metrics.peakMemMB()

	res := ExecuteResult{
		CPUTimeMs: cpuMs,
		MemoryMB:  peakMemMB,
		Stdout:    stdoutLW.String(),
		Stderr:    stderrLW.String(),
	}

	switch reason {
	case outputLimitReason:
		res.Verdict = VerdictOLE
		res.ExtraInfo = fmt.Sprintf("output limit exceeded (%d bytes max)", limits.OutputBytes)
	case cpuTimeLimitReason, wallTimeLimitReason:
		res.Verdict = VerdictTLE
		res.ExtraInfo = fmt.Sprintf(
			"%s (cpu %dms, cpu limit %dms, wall limit %dms)",
			reason,
			cpuMs,
			limits.CPUTimeMs,
			limits.WallTimeMs,
		)
	}
	return res
}
