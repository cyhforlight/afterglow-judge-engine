package httptransport

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewServer_RejectsInvalidOptions(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*ServerOptions)
		wantErr string
	}{
		{name: "missing address", mutate: func(opts *ServerOptions) { opts.Addr = "" }, wantErr: "HTTP address is required"},
		{name: "invalid port", mutate: func(opts *ServerOptions) { opts.Port = 0 }, wantErr: "HTTP port must be between"},
		{name: "invalid read timeout", mutate: func(opts *ServerOptions) { opts.ReadTimeout = 0 }, wantErr: "HTTP read timeout must be positive"},
		{name: "invalid write timeout", mutate: func(opts *ServerOptions) { opts.WriteTimeout = 0 }, wantErr: "HTTP write timeout must be positive"},
		{name: "invalid body limit", mutate: func(opts *ServerOptions) { opts.MaxBodyBytes = 0 }, wantErr: "HTTP request body limit must be positive"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := testServerOptions()
			tt.mutate(&opts)

			server, err := NewServer(opts, &mockJudgeService{}, slog.Default())
			assert.Nil(t, server)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func testServerOptions() ServerOptions {
	return ServerOptions{
		Addr:         "localhost",
		Port:         8080,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 2 * time.Minute,
		MaxBodyBytes: 256 * testBytesPerMiB,
	}
}
