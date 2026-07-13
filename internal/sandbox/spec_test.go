package sandbox

import (
	"context"
	"math"
	"testing"

	"github.com/containerd/containerd/v2/pkg/oci"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSandboxSecurityOpts_PinsContainerToCPU(t *testing.T) {
	spec := &oci.Spec{Linux: &specs.Linux{}}
	err := sandboxSecurityOpts(false, 7)(context.Background(), nil, nil, spec)
	require.NoError(t, err)

	require.NotNil(t, spec.Linux.Resources)
	require.NotNil(t, spec.Linux.Resources.CPU)
	assert.Equal(t, "7", spec.Linux.Resources.CPU.Cpus)
}

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
