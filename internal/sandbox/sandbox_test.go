package sandbox

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateExecuteLimits(t *testing.T) {
	tests := []struct {
		name    string
		limits  ResourceLimits
		wantErr string
	}{
		{
			name: "valid limits",
			limits: ResourceLimits{
				CPUTimeMs:   1000,
				WallTimeMs:  3000,
				MemoryMB:    256,
				OutputBytes: 1024,
			},
		},
		{
			name: "cpu time must be positive",
			limits: ResourceLimits{
				CPUTimeMs:   0,
				WallTimeMs:  3000,
				MemoryMB:    256,
				OutputBytes: 1024,
			},
			wantErr: "CPU time limit must be positive",
		},
		{
			name: "wall time must be positive",
			limits: ResourceLimits{
				CPUTimeMs:   1000,
				WallTimeMs:  0,
				MemoryMB:    256,
				OutputBytes: 1024,
			},
			wantErr: "wall time limit must be positive",
		},
		{
			name: "memory must be positive",
			limits: ResourceLimits{
				CPUTimeMs:   1000,
				WallTimeMs:  3000,
				MemoryMB:    0,
				OutputBytes: 1024,
			},
			wantErr: "memory limit must be positive",
		},
		{
			name: "output must be positive",
			limits: ResourceLimits{
				CPUTimeMs:   1000,
				WallTimeMs:  3000,
				MemoryMB:    256,
				OutputBytes: 0,
			},
			wantErr: "output limit must be positive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateExecuteLimits(tt.limits)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)
			assert.Equal(t, tt.wantErr, err.Error())
		})
	}
}

func TestResolveCwd(t *testing.T) {
	tests := []struct {
		name    string
		req     ExecuteRequest
		want    string
		wantOK  bool
		wantErr bool
	}{
		{
			name:   "explicit cwd wins",
			req:    ExecuteRequest{MountDir: &Mount{ContainerPath: "/sandbox"}, Cwd: stringPtr("/work")},
			want:   "/work",
			wantOK: true,
		},
		{
			name:   "mount dir becomes default cwd",
			req:    ExecuteRequest{MountDir: &Mount{ContainerPath: "/sandbox"}},
			want:   "/sandbox",
			wantOK: true,
		},
		{
			name:   "no mount and no cwd uses image default",
			req:    ExecuteRequest{},
			wantOK: false,
		},
		{
			name:    "relative cwd is rejected",
			req:     ExecuteRequest{Cwd: stringPtr("sandbox")},
			wantErr: true,
		},
		{
			name:    "relative mount path is rejected",
			req:     ExecuteRequest{MountDir: &Mount{ContainerPath: "sandbox"}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, err := resolveCwd(tt.req)
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func stringPtr(val string) *string {
	return &val
}
