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
	if strings.ContainsAny(secret, "\n\r\x00") {
		return fmt.Errorf("storing key: a key cannot contain a line break")
	}
	cmd := setKeyCommand(ref, secret)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("storing key: %s", strings.TrimSpace(string(out)))
	}
	// security -i exits 0 when the command it read fails, and says nothing, so
	// its exit status cannot tell a stored key from a refused one. Reading the
	// key back is the only way to know it is there.
	got, err := GetKey(ref)
	if err != nil || got != strings.TrimSpace(secret) {
		return fmt.Errorf("storing key: the keychain did not keep it (logos key set %s to retry)", ref)
	}
	return nil
}

// setKeyCommand stores the key through security's interactive mode, which
// reads the command from stdin.
//
// #198: passed as -w <secret>, the key sat in argv, where every process on the
// machine can read it with ps for as long as security runs. On stdin it is
// visible only to the process it was written to.
func setKeyCommand(ref, secret string) *exec.Cmd {
	// -U updates in place if the entry already exists.
	line := fmt.Sprintf("add-generic-password -s %s -a %s -w %s -U\n",
		securityQuote(keychainService), securityQuote(ref), securityQuote(secret))
	cmd := exec.Command("security", "-i")
	cmd.Stdin = strings.NewReader(line)
	return cmd
}

// securityQuote quotes one argument for security -i, whose tokenizer takes a
// double-quoted string with backslash escapes.
func securityQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func DeleteKey(ref string) error {
	cmd := exec.Command("security", "delete-generic-password", "-s", keychainService, "-a", ref)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("deleting key: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
