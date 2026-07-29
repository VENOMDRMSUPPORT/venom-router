package config

import (
	"errors"
	"testing"
)

func TestLoad_Precedence(t *testing.T) {
	tests := []struct {
		name    string
		env     string // "" means unset
		args    []string
		want    string
		wantErr bool
	}{
		{
			name: "default applies when nothing else is set",
			want: defaultBind,
		},
		{
			name: "env overrides default",
			env:  "10.0.0.5:9000",
			want: "10.0.0.5:9000",
		},
		{
			name: "flag overrides env and default",
			env:  "10.0.0.5:9000",
			args: []string{"-bind", "192.168.1.1:7000"},
			want: "192.168.1.1:7000",
		},
		{
			name:    "invalid bind address is rejected",
			args:    []string{"-bind", "not-a-valid-address"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(envBind, tt.env)

			got, err := Load(tt.args)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("Load() error = nil, want an error")
				}
				if !errors.Is(err, ErrInvalidBind) {
					t.Fatalf("Load() error = %v, want ErrInvalidBind", err)
				}
				if got != nil {
					t.Fatalf("Load() config = %+v, want nil on error", got)
				}
				return
			}

			if err != nil {
				t.Fatalf("Load() unexpected error: %v", err)
			}
			if got.Bind != tt.want {
				t.Fatalf("Load() Bind = %q, want %q", got.Bind, tt.want)
			}
		})
	}
}

// TestLoad_DataPlaneBind covers the optional data-plane bind: empty by default,
// env then flag precedence, non-loopback accepted (unlike the control bind),
// and a structurally invalid value rejected.
func TestLoad_DataPlaneBind(t *testing.T) {
	tests := []struct {
		name    string
		env     string // "" means unset
		args    []string
		want    string
		wantErr bool
	}{
		{name: "empty by default (shares the control listener)", want: ""},
		{name: "env sets it", env: "127.0.0.1:9090", want: "127.0.0.1:9090"},
		{
			name: "flag overrides env",
			env:  "127.0.0.1:9090",
			args: []string{"-data-plane-bind", "0.0.0.0:8080"},
			want: "0.0.0.0:8080",
		},
		{
			// The data plane MAY be non-loopback — it is not subject to the
			// control plane's loopback-only rule.
			name: "non-loopback accepted",
			env:  "203.0.113.7:8080",
			want: "203.0.113.7:8080",
		},
		{
			name:    "structurally invalid rejected",
			env:     "not-a-valid-address",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(envDataPlaneBind, tt.env)

			got, err := Load(tt.args)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidBind) {
					t.Fatalf("Load() error = %v, want ErrInvalidBind", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() unexpected error: %v", err)
			}
			if got.DataPlaneBind != tt.want {
				t.Fatalf("Load() DataPlaneBind = %q, want %q", got.DataPlaneBind, tt.want)
			}
		})
	}
}
