package router

import (
	"fmt"
	"os/exec"
	"strings"
)

// API keys live in the macOS Keychain, never in config.json and never in the
// vault. The vault is designed to be synced and shared; a key in it would
// eventually end up somewhere it should not be.

const keychainService = "logos"

func GetKey(ref string) (string, error) {
	out, err := exec.Command("security", "find-generic-password",
		"-s", keychainService, "-a", ref, "-w").Output()
	if err != nil {
		return "", fmt.Errorf("no key %q in keychain (add with: logos key set %s)", ref, ref)
	}
	return strings.TrimSpace(string(out)), nil
}

func SetKey(ref, secret string) error {
	// -U updates in place if the entry already exists.
	cmd := exec.Command("security", "add-generic-password",
		"-s", keychainService, "-a", ref, "-w", secret, "-U")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("storing key: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func DeleteKey(ref string) error {
	cmd := exec.Command("security", "delete-generic-password", "-s", keychainService, "-a", ref)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("deleting key: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
