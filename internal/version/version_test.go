package version

import "testing"

func TestNumber(t *testing.T) {
	for in, want := range map[string]int{
		"O8": 8, "o12": 12, " O3 ": 3, "O0": 0,
		"": 0, "O": 0, "8": 0, "v1.0": 0, "Ox": 0, "O-2": 0, "dev": 0,
	} {
		if got := Number(in); got != want {
			t.Errorf("Number(%q) = %d, want %d", in, got, want)
		}
	}
}
