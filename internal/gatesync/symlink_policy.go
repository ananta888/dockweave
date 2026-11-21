package gatesync

import "strings"

// Symlink policies for the gate snapshot (see Config.SymlinkPolicy).
const (
	SymlinkHash    = "hash"
	SymlinkIgnored = "ignored"
	SymlinkFail    = "fail"
)

var symlinkRank = map[string]int{"": 0, SymlinkHash: 1, SymlinkIgnored: 2, SymlinkFail: 3}

// ValidSymlinkPolicy reports whether v is a known policy (empty = default).
func ValidSymlinkPolicy(v string) bool {
	_, ok := symlinkRank[strings.TrimSpace(v)]
	return ok
}

// StricterSymlinkPolicy merges two connector settings for one workspace: the
// stricter one wins, and an unknown value counts as fail (fail closed).
func StricterSymlinkPolicy(a, b string) string {
	norm := func(v string) string {
		v = strings.TrimSpace(v)
		if !ValidSymlinkPolicy(v) {
			return SymlinkFail
		}
		return v
	}
	a, b = norm(a), norm(b)
	if symlinkRank[b] > symlinkRank[a] {
		return b
	}
	return a
}
