package legacy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Coder8124/logos/internal/vault"
)

// Vault finds a vault a 0.4 install left under the old name and records it as
// this machine's vault, returning a line saying so, or "" when there was
// nothing to carry.
//
// Recorded rather than resolved on every call: the pointer is what the desktop
// app and every host already read, so once written the old location is simply
// the chosen one and keeps working past 0.5.0 with nothing to rename. Nothing is
// recorded while LOGOS_VAULT is set — that run is scoped to one process, and a
// test's scratch vault must never decide where the real one is.
func Vault() string {
	if os.Getenv("LOGOS_VAULT") != "" || vault.Pointer() != "" {
		return ""
	}
	dir, from := oldPointer(), "the location 0.4 recorded"
	if dir == "" {
		h, err := os.UserHomeDir()
		if err != nil || isDir(filepath.Join(h, "logos")) || !isDir(filepath.Join(h, "brain")) {
			return ""
		}
		dir, from = filepath.Join(h, "brain"), "the 0.4 default"
	}
	if err := vault.Record(dir); err != nil {
		// Unrecorded, this process still uses it: a failed write must not be
		// the reason someone's memory looks empty.
		os.Setenv("LOGOS_VAULT", dir)
		return fmt.Sprintf("using the vault at %s, %s, but could not record it (%v) — run `logos setup --vault %s`", dir, from, err, dir)
	}
	return fmt.Sprintf("using the vault at %s, %s, and recorded it as this machine's vault", dir, from)
}

func oldPointer() string {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(cfg, "brain", "vault-path"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
