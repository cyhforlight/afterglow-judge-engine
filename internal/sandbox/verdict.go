package sandbox

import "fmt"

type executionOutput struct {
	stdout     string
	stderr     string
	overflowed bool
}

func buildVerdict(outcome executionOutcome, output executionOutput, limits ResourceLimits) ExecuteResult {
	cpuMs := outcome.metrics.cpuMillis()
	peakMemMB := outcome.metrics.peakMemMB()

	res := ExecuteResult{
		ExitCode:  int(outcome.exitCode),
		Stdout:    output.stdout,
		Stderr:    output.stderr,
		CPUTimeMs: cpuMs,
		MemoryMB:  peakMemMB,
	}

	switch {
	case outcome.reason == stopOutputLimit || (outcome.reason == 0 && output.overflowed):
		res.Verdict = VerdictOLE
		res.ExtraInfo = fmt.Sprintf("output limit exceeded (%d bytes max)", limits.OutputBytes)

	case outcome.reason == stopCPUTime || outcome.reason == stopWallTime:
		res.Verdict = VerdictTLE
		res.ExtraInfo = fmt.Sprintf(
			"%s (cpu %dms, cpu limit %dms, wall limit %dms)",
			outcome.reason,
			cpuMs,
			limits.CPUTimeMs,
			limits.WallTimeMs,
		)

	case outcome.metrics.oomKillDetected || (outcome.exitCode != 0 && outcome.metrics.oomDetected):
		res.Verdict = VerdictMLE
		res.ExtraInfo = fmt.Sprintf("memory limit exceeded (peak %dMB, limit %dMB)", peakMemMB, limits.MemoryMB)

	case cpuMs >= limits.CPUTimeMs:
		res.Verdict = VerdictTLE
		res.ExtraInfo = fmt.Sprintf("CPU time exceeded: %dms >= %dms", cpuMs, limits.CPUTimeMs)

	case outcome.exitCode == 0:
		res.Verdict = VerdictOK

	default:
		res.Verdict = VerdictRE
		res.ExtraInfo = output.stderr
	}
	return res
}
