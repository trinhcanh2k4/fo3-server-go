package game

// Third password (player-lock password), the gate of the player lock
// feature. Stored ONLY as an Argon2id hash at state level
// (state["third_pass_hash"]); the fail counter lives at
// state["_3rdpass_fail"]. Client contract: see internal/api/thirdpass.go.

import (
	"strings"

	"fo3server/internal/store"
)

// HasThirdPass reports whether a lock password has been set.
func HasThirdPass(st State) bool {
	return Str(st["third_pass_hash"]) != ""
}

// VerifyThirdPass checks cand against the stored hash. With no lock
// password set it never verifies (unlike the second password, whose
// fx behaviour accepts anything while unset).
func VerifyThirdPass(st State, cand string) bool {
	hash := Str(st["third_pass_hash"])
	if hash == "" {
		return false
	}
	return store.CheckPassword(strings.TrimSpace(cand), hash)
}

// SetThirdPass stores cand as a hash and resets the fail counter.
func SetThirdPass(st State, cand string) {
	cand = strings.TrimSpace(cand)
	if hash, err := store.HashPassword(cand); err == nil {
		st["third_pass_hash"] = hash
	}
	st["_3rdpass_fail"] = 0
}
