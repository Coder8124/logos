package mcpserver

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	vaultpkg "github.com/Coder8124/brain/internal/vault"
)

// pairing.go: the one secret the local HTTP/WebSocket transport needs before
// it will talk to anyone.
//
// Any webpage a user has open can send a request to localhost — the classic
// drive-by-localhost / DNS-rebinding attack — so binding to 127.0.0.1 alone is
// not a security boundary, only a network one. A bearer token that never
// leaves the machine except into the one extension the user pastes it into is
// the same shape Ollama's and Docker Desktop's local APIs use, and it is
// checked alongside (not instead of) the Origin allowlist in http.go — either
// one failing is a rejection.

// tokenPath is where the token lives: .brain/, alongside config.json and
// flavor.json, not the vault proper. It is local machine state, not something
// a rebuild-from-vault promise covers — deleting it just means re-pairing.
func tokenPath(vault string) string {
	return filepath.Join(vault, ".brain", "webbridge.json")
}

type pairingFile struct {
	Token string `json:"token"`
}

// LoadOrCreateToken returns the vault's pairing token, minting one on first
// use. Stable across restarts so `brain mcp serve --http` doesn't force the
// user to re-paste a new token into the extension every time they start it.
func LoadOrCreateToken(vault string) (string, error) {
	p := tokenPath(vault)
	if b, err := os.ReadFile(p); err == nil {
		var f pairingFile
		if err := json.Unmarshal(b, &f); err == nil && f.Token != "" {
			return f.Token, nil
		}
		// Fall through and regenerate: a truncated or corrupt file is not worth
		// failing startup over.
	}

	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating pairing token: %w", err)
	}
	token := hex.EncodeToString(buf)

	if err := vaultpkg.MkdirPrivate(filepath.Dir(p)); err != nil {
		return "", fmt.Errorf("creating .brain: %w", err)
	}
	b, err := json.Marshal(pairingFile{Token: token})
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return "", fmt.Errorf("writing pairing token: %w", err)
	}
	return token, nil
}

// HasToken reports whether a vault has ever paired, for `brain doctor` — it
// must never itself mint one; a health check that has a side effect of
// creating a secret is a bug waiting to be filed.
func HasToken(vault string) bool {
	b, err := os.ReadFile(tokenPath(vault))
	if err != nil {
		return false
	}
	var f pairingFile
	return json.Unmarshal(b, &f) == nil && f.Token != ""
}
