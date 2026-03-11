package service

import "afterglow-judge-engine/internal/model"

// selectWorstVerdict aggregates test case verdicts using priority:
// OLE > MLE > TLE > RE > UKE > WA > OK.
func selectWorstVerdict(cases []model.JudgeCaseResult) model.Verdict {
	if len(cases) == 0 {
		return model.VerdictUKE
	}

	highestRuntime := model.VerdictUnknown
	hasWA := false

	for _, caseResult := range cases {
		if isRuntimeVerdict(caseResult.Verdict) {
			if runtimeSeverity(caseResult.Verdict) > runtimeSeverity(highestRuntime) {
				highestRuntime = caseResult.Verdict
			}
			continue
		}

		if caseResult.Verdict == model.VerdictWA {
			hasWA = true
		}
	}

	if highestRuntime != model.VerdictUnknown {
		return highestRuntime
	}
	if hasWA {
		return model.VerdictWA
	}
	return model.VerdictOK
}

func isRuntimeVerdict(verdict model.Verdict) bool {
	switch verdict {
	case model.VerdictOLE, model.VerdictMLE, model.VerdictTLE, model.VerdictRE, model.VerdictUKE:
		return true
	default:
		return false
	}
}

func runtimeSeverity(verdict model.Verdict) int {
	switch verdict {
	case model.VerdictOLE:
		return 5
	case model.VerdictMLE:
		return 4
	case model.VerdictTLE:
		return 3
	case model.VerdictRE:
		return 2
	case model.VerdictUKE:
		return 1
	default:
		return 0
	}
}
