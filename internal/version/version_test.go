package version

import "testing"

func TestNumber(t *testing.T) {
	for in, want := range map[string]int{
		"O8": 8, "o12": 12, " O3 ": 3, "O0": 0, "O8.1": 8,
		"": 0, "O": 0, "8": 0, "v1.0": 0, "Ox": 0, "O-2": 0, "dev": 0, "O8.": 0, "O8.x": 0, "O8.1.2": 0,
	} {
		if got := Number(in); got != want {
			t.Errorf("Number(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestNewer(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"O9", "O8", true},
		{"O8.1", "O8", true},
		{"O8", "O8.1", false},
		{"O8.2", "O8.10", false},
		{"O9", "O8.5", true},
		{"O8", "O8", false},
		{"O8.1", "O8.1", false},
		{"O8", "dev", true},
		{"dev", "O8", false},
	} {
		if got := Newer(tc.a, tc.b); got != tc.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
