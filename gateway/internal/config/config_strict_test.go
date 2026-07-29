package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRejectsUnknownTopLevelField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.yaml")
	if err := os.WriteFile(path, []byte("global:\n  routing_enabled: true\nmystery: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "field mystery not found") {
		t.Fatalf("Load error = %v, want unknown top-level field", err)
	}
}

func TestValidateRoutingPolicyRequiresProtocolShape(t *testing.T) {
	enabled := false
	err := ValidateRoutingPolicy(&File{
		Global: Global{RoutingEnabled: &enabled},
		Clients: []Client{{
			Name: "claude-code",
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "protocol_shape must be explicitly") {
		t.Fatalf("ValidateRoutingPolicy() error = %v", err)
	}
}

func TestValidateRoutingPolicyAllowsUnselectedModelOnlyWhileRoutingOff(
	t *testing.T,
) {
	off := false
	file := &File{
		Global: Global{RoutingEnabled: &off},
		Clients: []Client{{
			Name:          "claude-code",
			Enabled:       true,
			ProtocolShape: "anthropic",
		}},
	}
	if err := ValidateRoutingPolicy(file); err != nil {
		t.Fatalf("routing-off unselected config = %v", err)
	}
	on := true
	file.Global.RoutingEnabled = &on
	if err := ValidateRoutingPolicy(file); err == nil {
		t.Fatal("routing-on unselected config unexpectedly passed")
	}
}

func TestValidateRoutingPolicyValidatesGlobalAuthAuthorities(t *testing.T) {
	off := false
	for _, tc := range []struct {
		name    string
		auth    map[string]string
		wantErr string
	}{
		{
			name: "anthropic allowed",
			auth: map[string]string{"anthropic": "${ANTHROPIC_API_KEY}"},
		},
		{
			name:    "openrouter rejected",
			auth:    map[string]string{"openrouter": "${OPENROUTER_API_KEY}"},
			wantErr: "openrouter-switch auth set-key",
		},
		{
			name:    "unknown rejected",
			auth:    map[string]string{"future": "value"},
			wantErr: `global.auth key "future" is unsupported`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRoutingPolicy(&File{
				Global: Global{
					RoutingEnabled: &off,
					Auth:           tc.auth,
				},
			})
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateRoutingPolicy() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf(
					"ValidateRoutingPolicy() error = %v, want %q",
					err,
					tc.wantErr,
				)
			}
		})
	}
}
