package mtprotoedge

import "testing"

// TestShouldExcludeDeviceMatchesAllSessionsOfDevice guards the phone-call
// "stop ringing" fix: the accepting device (identified by its perm/business
// auth key) must be excluded across ALL its connections, not just the one
// session that carried the accept — otherwise the stop-ringing phoneCallDiscarded
// leaks onto the device's other connections and kills the call it just accepted
// (the "B answers → instantly Failed to connect" asymmetry).
func TestShouldExcludeDeviceMatchesAllSessionsOfDevice(t *testing.T) {
	device := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
	other := [8]byte{9, 9, 9, 9, 9, 9, 9, 9}

	// Two connections of the SAME device but different raw keys / sessions —
	// exactly the OwpenGram multi-connection (dc 1..5 → one server) shape.
	connA := &Conn{authKeyID: [8]byte{0xA}, sessionID: 111}
	connA.SetBusinessAuthKeyID(device)
	connB := &Conn{authKeyID: [8]byte{0xB}, sessionID: 222}
	connB.SetBusinessAuthKeyID(device)
	// A connection of a DIFFERENT device (the real "other device" that should
	// still receive the stop-ringing).
	connOther := &Conn{authKeyID: [8]byte{0xC}, sessionID: 333}
	connOther.SetBusinessAuthKeyID(other)

	if !shouldExcludeDevice(connA, device) {
		t.Fatal("accepting device's connection A must be excluded")
	}
	if !shouldExcludeDevice(connB, device) {
		t.Fatal("accepting device's connection B (other session) must ALSO be excluded")
	}
	if shouldExcludeDevice(connOther, device) {
		t.Fatal("a different device must NOT be excluded")
	}
}

func TestShouldExcludeDeviceZeroKeyExcludesNothing(t *testing.T) {
	c := &Conn{authKeyID: [8]byte{0xA}, sessionID: 111}
	c.SetBusinessAuthKeyID([8]byte{1, 2, 3})
	if shouldExcludeDevice(c, [8]byte{}) {
		t.Fatal("zero business auth key must exclude nothing")
	}
}

func TestShouldExcludeDeviceUnresolvedBusinessKeyNotExcluded(t *testing.T) {
	// A connection whose business auth key isn't resolved yet must not be
	// matched (can't prove it's the accepting device).
	c := &Conn{authKeyID: [8]byte{0xA}, sessionID: 111}
	if shouldExcludeDevice(c, [8]byte{1, 2, 3, 4, 5, 6, 7, 8}) {
		t.Fatal("connection with unresolved business auth key must not be excluded")
	}
}
