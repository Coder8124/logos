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

//go:embed gitleaks.go
var gitleaksSrc []byte

//go:embed pii.go
var piiSrc []byte

//go:embed keywords.go
var keywordsSrc []byte

// ruleSources is every file that decides what is masked. A new file holding
// rules must be added here: TestEveryRuleFileIsHashedIntoRules fails for one
// that is not, because a copy masked under old rules is only re-masked when
// Rules changes, and a rule that does not move it never reaches old copies.
var ruleSources = map[string][]byte{
	"secret.go":   secretSrc,
	"private.go":  privateSrc,
	"gitleaks.go": gitleaksSrc,
	"pii.go":      piiSrc,
	"keywords.go": keywordsSrc,
}

// Rules names what this build masks: a hash of the sources that decide it.
// A copy masked under other rules may hold what these now mask, and a version
// number cannot say so — it changes every release, when the rules mostly do
// not, and it stays put on a dev build whose rules did. Any edit to these
// files, a comment included, changes it; that costs one full pass per copy,
// which is the safe direction to be wrong in.
var Rules = hashSources(ruleSources)

func hashSources(srcs map[string][]byte) string {
	h := sha256.New()
	// Fixed order: a map's is not, and the hash must not vary between runs.
	for _, name := range []string{"secret.go", "private.go", "gitleaks.go", "pii.go", "keywords.go"} {
		h.Write([]byte(name))
		h.Write(srcs[name])
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}
