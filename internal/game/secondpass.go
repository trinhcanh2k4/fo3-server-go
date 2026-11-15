package game

// Second password (mat khau cap 2), ported from fx ep_2ndpass_*.
// Per the rewrite's security design the value is stored ONLY as an
// Argon2id hash (state["second_pass_hash"]). Legacy plaintext values
// imported from the Python state keep working and are migrated to a
// hash on the first successful check.

import (
	"strings"

	"fo3server/internal/store"
)

// HasSecondPass mirrors fx: a pass lives at state level, on the
// default coach, or as a stored hash.
func HasSecondPass(st State) bool {
	if Str(st["second_pass_hash"]) != "" || Str(st["second_pass"]) != "" {
		return true
	}
	if coach := CoachBySlot(st, DefaultSlot(st)); coach != nil {
		return Str(coach["second_pass"]) != ""
	}
	return false
}

func legacySecondPass(st State) string {
	if v := strings.TrimSpace(Str(st["second_pass"])); v != "" {
		return v
	}
	if coach := CoachBySlot(st, DefaultSlot(st)); coach != nil {
		return strings.TrimSpace(Str(coach["second_pass"]))
	}
	return ""
}

// VerifySecondPass checks cand; on a successful legacy-plaintext
// match it migrates the stored value to a hash in place (the caller
// saves the state).
func VerifySecondPass(st State, cand string) bool {
	cand = strings.TrimSpace(cand)
	if hash := Str(st["second_pass_hash"]); hash != "" {
		return store.CheckPassword(cand, hash)
	}
	real := legacySecondPass(st)
	if real == "" {
		return true // no pass set: fx accepts anything
	}
	if cand != real {
		return false
	}
	SetSecondPass(st, cand)
	return true
}

// SetSecondPass stores cand as a hash and clears any plaintext.
func SetSecondPass(st State, cand string) {
	cand = strings.TrimSpace(cand)
	if hash, err := store.HashPassword(cand); err == nil {
		st["second_pass_hash"] = hash
	}
	delete(st, "second_pass")
	if coach := CoachBySlot(st, DefaultSlot(st)); coach != nil {
		delete(coach, "second_pass")
	}
	st["_2ndpass_fail"] = 0
}

// ClearSecondPass removes the pass everywhere (fx reset).
func ClearSecondPass(st State) {
	delete(st, "second_pass")
	delete(st, "second_pass_hash")
	for _, coach := range Coaches(st) {
		delete(coach, "second_pass")
	}
	st["_2ndpass_fail"] = 0
}
