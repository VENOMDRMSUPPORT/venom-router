package domain

import "testing"

func TestDeriveDisplayStatus_Precedence(t *testing.T) {
	cases := []struct {
		name           string
		account        Account
		cooldownActive bool
		want           DisplayStatus
	}{
		{
			name: "disconnected wins over a healthy health_state",
			account: Account{
				ConnectionState: ConnectionDisconnected,
				HealthState:     HealthHealthy,
			},
			want: DisplayDisconnected,
		},
		{
			name: "stopped wins over reauth_in_progress",
			account: Account{
				ConnectionState:  ConnectionStopped,
				ReauthInProgress: true,
				HealthState:      HealthHealthy,
			},
			want: DisplayStopped,
		},
		{
			name: "connecting wins over cooldown",
			account: Account{
				ConnectionState: ConnectionConnecting,
				HealthState:     HealthUnknown,
			},
			cooldownActive: true,
			want:           DisplayConnecting,
		},
		{
			name: "reauthenticating wins over cooling_down",
			account: Account{
				ConnectionState:  ConnectionConnected,
				ReauthInProgress: true,
				HealthState:      HealthHealthy,
			},
			cooldownActive: true,
			want:           DisplayReauthenticating,
		},
		{
			name: "cooling_down wins over health_state",
			account: Account{
				ConnectionState: ConnectionConnected,
				HealthState:     HealthHealthy,
			},
			cooldownActive: true,
			want:           DisplayCoolingDown,
		},
		{
			name: "health_state expired",
			account: Account{
				ConnectionState: ConnectionConnected,
				HealthState:     HealthExpired,
			},
			want: DisplayExpired,
		},
		{
			name: "health_state unavailable",
			account: Account{
				ConnectionState: ConnectionConnected,
				HealthState:     HealthUnavailable,
			},
			want: DisplayUnavailable,
		},
		{
			name: "health_state degraded",
			account: Account{
				ConnectionState: ConnectionConnected,
				HealthState:     HealthDegraded,
			},
			want: DisplayDegraded,
		},
		{
			name: "health_state healthy",
			account: Account{
				ConnectionState: ConnectionConnected,
				HealthState:     HealthHealthy,
			},
			want: DisplayHealthy,
		},
		{
			name: "health_state unknown falls through to unknown",
			account: Account{
				ConnectionState: ConnectionConnected,
				HealthState:     HealthUnknown,
			},
			want: DisplayUnknown,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DeriveDisplayStatus(c.account, c.cooldownActive)
			if got != c.want {
				t.Fatalf("DeriveDisplayStatus() = %s, want %s", got, c.want)
			}
		})
	}
}
