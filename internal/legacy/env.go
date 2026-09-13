// Package legacy keeps an install from before the rename to logos working
// through 0.4.x. Everything here reads an old name so that nobody's memory
// disappears on upgrade, and all of it goes before 0.5.0.
package legacy

import (
	"os"
	"sort"
	"strings"
)

// Env copies every BRAIN_* variable to its LOGOS_* name when the new name is
// not already set, and returns the old names it copied so the caller can say
// so. Host configs written by 0.4 setup pin BRAIN_VAULT; copying once at start
// covers every reader instead of teaching each one two names.
func Env() []string {
	var copied []string
	for _, kv := range os.Environ() {
		key, val, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(key, "BRAIN_") {
			continue
		}
		next := "LOGOS_" + strings.TrimPrefix(key, "BRAIN_")
		if _, set := os.LookupEnv(next); set {
			continue
		}
		os.Setenv(next, val)
		copied = append(copied, key)
	}
	sort.Strings(copied)
	return copied
}
