package service

import (
	"errors"
	"fmt"
	"math"

	"afterglow-judge-engine/internal/execution"
)

func wallTimeLimitMs(cpuTimeLimitMs int) (int, error) {
	if cpuTimeLimitMs <= 0 {
		return 0, errors.New("time limit must be positive")
	}
	if cpuTimeLimitMs > math.MaxInt/execution.WallTimeMultiplier {
		return 0, fmt.Errorf("time limit too large: %dms", cpuTimeLimitMs)
	}
	return cpuTimeLimitMs * execution.WallTimeMultiplier, nil
}
