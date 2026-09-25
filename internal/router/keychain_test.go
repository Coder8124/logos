package router

import (
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// #198: every process on the machine can read argv with ps. The key has to
// reach security some other way.
func TestStoringAKeyDoesNotPutItOnTheCommandLine(t *testing.T) {
	secret := "sk-ant-not-a-real-key"
	cmd := setKeyCommand("anthropic", secret)
	for _, arg := range cmd.Args {
		if strings.Contains(arg, secret) {
			t.Fatalf("the key is in argv: %q", cmd.Args)
		}
	}
	in, err := io.ReadAll(cmd.Stdin)
	if err != nil || !strings.Contains(string(in), secret) {
		t.Fatalf("the key does not reach security on stdin: %q", in)
	}
}

// The quoting has to survive security's own tokenizer, or a key with a quote
// or backslash in it is stored as something else. Run against a throwaway
// keychain so the user's login keychain is never touched.
func TestAKeyWithQuotesAndBackslashesIsStoredExactly(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("security is macOS only")
	}
	kc := filepath.Join(t.TempDir(), "t.keychain")
	if out, err := exec.Command("security", "create-keychain", "-p", "x", kc).CombinedOutput(); err != nil {
		t.Skipf("cannot create a keychain here: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("security", "delete-keychain", kc).Run() })

	secret := `a b"c\d$x 'e'`
	line := "add-generic-password -s " + securityQuote(keychainService) + " -a " +
		securityQuote("ref") + " -w " + securityQuote(secret) + " -U " + securityQuote(kc) + "\n"
	cmd := exec.Command("security", "-i")
	cmd.Stdin = strings.NewReader(line)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("security -i: %v: %s", err, out)
	}
	got, err := exec.Command("security", "find-generic-password", "-s", keychainService, "-a", "ref", "-w", kc).Output()
	if err != nil {
		t.Fatalf("reading the key back: %v", err)
	}
	if strings.TrimSuffix(string(got), "\n") != secret {
		t.Fatalf("stored %q, want %q", got, secret)
	}
}
