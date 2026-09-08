package contextpack

import "github.com/Coder8124/brain/internal/untrusted"

// inline, block and boundary used to be defined here. They moved to
// internal/untrusted so a second renderer — internal/procedure's
// before_you_try output — gets the same guarantee against forged frame
// instead of a copy of it. See that package's doc comment for the full
// rationale; these three names are kept as thin wrappers so every call site
// and test in this package is untouched.
func inline(s string) string   { return untrusted.Inline(s) }
func block(body string) string { return untrusted.Block(body) }

const boundary = untrusted.Boundary
