package sandbox

import (
	"testing"

	"github.com/containerd/containerd/v2/pkg/oci"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSandboxSecurityOpts_PinsContainerToCPU(t *testing.T) {
	spec := &oci.Spec{Linux: &specs.Linux{}}
	err := sandboxSecurityOpts(false, 7)(t.Context(), nil, nil, spec)
	require.NoError(t, err)

	require.NotNil(t, spec.Linux.Resources)
	require.NotNil(t, spec.Linux.Resources.CPU)
	assert.Equal(t, "7", spec.Linux.Resources.CPU.Cpus)
}

func TestMountSpecOpts_ConfiguresMountAndWorkingDirectory(t *testing.T) {
	opts := mountSpecOpts(&Mount{HostPath: "/tmp/work", ContainerPath: "/sandbox"})
	spec := &oci.Spec{Process: &specs.Process{}}
	for _, opt := range opts {
		require.NoError(t, opt(t.Context(), nil, nil, spec))
	}

	require.Len(t, spec.Mounts, 1)
	assert.Equal(t, "/tmp/work", spec.Mounts[0].Source)
	assert.Equal(t, "/sandbox", spec.Mounts[0].Destination)
	assert.Equal(t, "/sandbox", spec.Process.Cwd)
}
