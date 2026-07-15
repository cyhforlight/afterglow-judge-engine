package sandbox

import (
	"context"
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

func TestMountSpecOpts_SetsContainerPathAsCwd(t *testing.T) {
	opts := mountSpecOpts(&Mount{HostPath: "/tmp/work", ContainerPath: "/sandbox"})
	require.Len(t, opts, 2)

	spec := &oci.Spec{Process: &specs.Process{}}
	require.NoError(t, opts[1](t.Context(), nil, nil, spec))
	assert.Equal(t, "/sandbox", spec.Process.Cwd)
}
