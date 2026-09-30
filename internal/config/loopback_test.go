package config

import "testing"

// The portable edition uses this to tell "the MinIO the standard edition's
// compose file starts" (never running there) apart from a real, external
// object store that must be left configured -- see Load's Edition block.
func TestIsLoopbackHostPort(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"127.0.0.1:9000", true},
		{"127.0.0.1", true},
		{"127.5.6.7:9000", true},
		{"localhost:9000", true},
		{"LOCALHOST:9000", true},
		{"::1", true},
		{"[::1]:9000", true},
		{"", false},
		{"minio:9000", false},
		{"s3.amazonaws.com", false},
		{"s3.eu-central-1.amazonaws.com:443", false},
		{"192.168.0.10:9000", false},
		{"10.0.0.5:9000", false},
		{"not a host:port:at all", false},
	} {
		if got := isLoopbackHostPort(tc.in); got != tc.want {
			t.Errorf("isLoopbackHostPort(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
