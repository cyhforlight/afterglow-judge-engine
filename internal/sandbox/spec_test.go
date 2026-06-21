package sandbox

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryBytesFromMB_RejectsInvalidLimits(t *testing.T) {
	tests := []struct {
		name    string
		memory  int
		wantErr string
	}{
		{
			name:    "non-positive",
			memory:  0,
			wantErr: "memory limit must be positive",
		},
		{
			name:    "overflow",
			memory:  math.MaxInt,
			wantErr: "memory limit too large",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := memoryBytesFromMB(tt.memory)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
