package service

import "context"

const (
	testlibHeaderKey = "testlib.h"

	checkerSourceFileName   = "checker.cpp"
	checkerArtifactFileName = "checker"
	checkerInputFileName    = "input.txt"
	checkerOutputFileName   = "output.txt"
	checkerAnswerFileName   = "answer.txt"

	checkerCPUTimeLimitMs = 3000
	checkerMemoryLimitMB  = 256
)

// ResourceStore provides read-only access to internal checker resources.
type ResourceStore interface {
	Get(ctx context.Context, key string) ([]byte, error)
}
