package datafactory

import "testing"

func TestFormatBytes(t *testing.T) {
	for n, want := range map[int]string{0: "0 B", 1023: "1023 B", 1536: "1.5 KB", 2048: "2 KB", 1100: "1.07 KB", 3 << 20: "3 MB"} {
		if got := formatBytes(n); got != want {
			t.Errorf("formatBytes(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestDotnetFloat(t *testing.T) {
	for f, want := range map[float64]string{1.5: "1.5", 1e20: "1E+20", 1e-7: "1E-07", 123456789012345: "123456789012345", 1e15: "1E+15", 0.0001: "0.0001", 0: "0"} {
		if got := dotnetFloat(f, 64); got != want {
			t.Errorf("dotnetFloat(%v) = %q, want %q", f, got, want)
		}
	}
}
