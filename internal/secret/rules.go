package secret

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
)

//go:embed secret.go
var secretSrc []byte

//go:embed private.go
var privateSrc []byte

// Rules names what this build masks: a hash of the sources that decide it.
// A copy masked under other rules may hold what these now mask, and a version
// number cannot say so — it changes every release, when the rules mostly do
// not, and it stays put on a dev build whose rules did. Any edit to these
// files, a comment included, changes it; that costs one full pass per copy,
// which is the safe direction to be wrong in.
var Rules = func() string {
	h := sha256.New()
	h.Write(secretSrc)
	h.Write(privateSrc)
	return hex.EncodeToString(h.Sum(nil))[:16]
}()
