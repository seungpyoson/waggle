package spawn

import (
	"runtime"
	"testing"
)

// TestDetect_ReturnsNonUnknown — on macOS, detects Terminal.app or iTerm2
func TestDetect_ReturnsNonUnknown(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("skipping macOS-specific test")
	}

	term := Detect()
	if term == Unknown {
		t.Error("Detect() should return TerminalApp or ITerm2 on macOS, got Unknown")
	}
	if term != TerminalApp && term != ITerm2 {
		t.Errorf("Detect() = %v, want TerminalApp or ITerm2", term)
	}
}

// TestDetect_EnvOverride — TERM_PROGRAM controls detection
func TestDetect_EnvOverride(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		want     Terminal
	}{
		{
			name:     "Apple_Terminal",
			envValue: "Apple_Terminal",
			want:     TerminalApp,
		},
		{
			name:     "iTerm.app",
			envValue: "iTerm.app",
			want:     ITerm2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TERM_PROGRAM", tt.envValue)
			got := Detect()
			if got != tt.want {
				t.Errorf("Detect() with TERM_PROGRAM=%q = %v, want %v", tt.envValue, got, tt.want)
			}
		})
	}
}

