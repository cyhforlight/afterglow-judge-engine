package service

import (
	"context"
	"io/fs"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"testing/synctest"
	"time"

	"afterglow-judge-engine/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type gatedReadFS struct {
	fs.ReadFileFS
	release <-chan struct{}
	active  atomic.Int32
}

func (f *gatedReadFS) ReadFile(name string) ([]byte, error) {
	f.active.Add(1)
	defer f.active.Add(-1)

	<-f.release
	return f.ReadFileFS.ReadFile(name)
}

func newGatedReadFS(release <-chan struct{}) *gatedReadFS {
	return &gatedReadFS{
		ReadFileFS: fstest.MapFS{
			"test.in":  &fstest.MapFile{Data: []byte("input")},
			"test.out": &fstest.MapFile{Data: []byte("output")},
		},
		release: release,
	}
}

func externalFileJudgeRequest() model.JudgeRequest {
	return baseJudgeRequest(model.JudgeTestCase{
		InputFile:          "test.in",
		ExpectedOutputFile: "test.out",
	})
}

// TestJudgeEngine_ConcurrencyLimit verifies that materialization is covered by the Judge limit.
func TestJudgeEngine_ConcurrencyLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const maxConcurrent = 2
		const numRequests = 5

		release := make(chan struct{})
		externalFS := newGatedReadFS(release)
		languageModule := newFakeLanguage()
		checkerModule := newFakeChecker()

		engine := newJudgeEngine(
			languageModule,
			checkerModule,
			externalFS,
			maxConcurrent,
			model.DefaultJudgeLimits(),
		)
		req := externalFileJudgeRequest()
		results := make([]model.JudgeResult, numRequests)
		judgeErrors := make([]error, numRequests)

		for i := range numRequests {
			go func() {
				results[i], judgeErrors[i] = engine.Judge(t.Context(), req)
			}()
		}

		synctest.Wait()
		assert.Equal(t, int32(maxConcurrent), externalFS.active.Load())
		assert.Empty(t, languageModule.compiler.sources)
		assert.Equal(t, maxConcurrent, checkerModule.materializeCalls())

		close(release)
		synctest.Wait()
		for i, result := range results {
			require.NoError(t, judgeErrors[i])
			assert.Equal(t, model.JudgeStatusOK, result.Status)
		}
	})
}

// TestJudgeEngine_ConcurrencyTimeout verifies context cancellation while waiting for capacity.
func TestJudgeEngine_ConcurrencyTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		externalFS := newGatedReadFS(release)

		engine := newJudgeEngine(
			newFakeLanguage(),
			newFakeChecker(),
			externalFS,
			1,
			model.DefaultJudgeLimits(),
		)
		req := externalFileJudgeRequest()

		go engine.Judge(t.Context(), req)
		synctest.Wait()
		assert.Equal(t, int32(1), externalFS.active.Load())

		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()

		result, err := engine.Judge(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, model.JudgeStatusSystemError, result.Status)
		assert.Contains(t, result.Compile.Log, "timed out while waiting for capacity")

		close(release)
	})
}
