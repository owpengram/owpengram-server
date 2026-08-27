package domain

import "strings"

// ReservedUsernameSet is a case-insensitive lookup of usernames self-service
// registration should never be allowed to claim (see config.ReservedUsernames
// -- brand-adjacent/staff-sounding words, or an operator's own real handle).
// Admin-panel-driven username assignment does not consult this at all: an
// operator setting one of these on an account on purpose is not the
// squatting it exists to stop.
type ReservedUsernameSet map[string]bool

// NewReservedUsernameSet builds a set from config.ReservedUsernames. A nil
// or empty input is a valid, empty set (Contains always false) -- nothing
// reserved beyond what's already taken by another account.
func NewReservedUsernameSet(names []string) ReservedUsernameSet {
	set := make(ReservedUsernameSet, len(names))
	for _, n := range names {
		n = strings.ToLower(strings.TrimSpace(n))
		if n != "" {
			set[n] = true
		}
	}
	return set
}

// Contains reports whether username (compared case-insensitively, leading
// "@" ignored) is reserved.
func (s ReservedUsernameSet) Contains(username string) bool {
	username = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(username), "@")))
	return s[username]
}
