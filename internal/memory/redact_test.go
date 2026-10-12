package memory

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRememberDoesNotWriteAPastedCredential(t *testing.T) {
	db, dir := store(t)
	const key = "hf_Qz8Rt5Uv2Wx9Ya6Bc3De0Fg7Hi4Jk1Lm8No5Pq"

	r, err := Store(db, nil, "", &Memory{
		Text: "the eval job reads HF_TOKEN, currently " + key, Kind: Fact, Source: "mcp",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Redactions) == 0 {
		t.Fatal("the credential was masked without the receipt saying so")
	}
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Ext(path) == ".db" {
			return nil
		}
		b, _ := os.ReadFile(path)
		if strings.Contains(string(b), key) {
			t.Errorf("the credential reached the vault in %s", path)
		}
		return nil
	})
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM memories WHERE text LIKE ?`, "%"+key+"%").Scan(&n)
	if n != 0 {
		t.Error("the credential is in the index")
	}
}

// The empty check ran on the text as given, before masking, so a memory that
// was nothing but a pasted key cleared it and was stored as the memory
// "[REDACTED]" — a fact that says nothing, ranked and packed like any other.
func TestAMemoryThatIsOnlyACredentialIsRefusedNotStoredAsTheMarker(t *testing.T) {
	db, _ := store(t)
	_, err := Store(db, nil, "", &Memory{Text: "  ghp_0123456789abcdefghijklmnopqrstuvwxyzAB ", Kind: Fact, Source: "mcp"})
	if err == nil || !strings.Contains(err.Error(), "credential") {
		t.Errorf("want an error that names the credential, got %v", err)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM memories`).Scan(&n)
	if n != 0 {
		t.Errorf("stored %d memory(ies) from a bare key", n)
	}
}

// The emptiness check after masking looked only for the credential marker, so
// a memory that was nothing but a <private> span — or a span and a key — was
// stored as the fact "[private]", embedded, ranked and recalled, with a receipt
// that read as a success.
func TestAMemoryThatIsOnlyAPrivateSpanIsRefusedNotStoredAsTheMarker(t *testing.T) {
	db, _ := store(t)
	for _, text := range []string{
		"<private>my bank pin is 4417</private>",
		"<private>pin 4417</private> ghp_0123456789abcdefghijklmnopqrstuvwxyzAB.",
	} {
		if _, err := Store(db, nil, "", &Memory{Text: text, Kind: Fact, Source: "mcp"}); !errors.Is(err, ErrOnlySecret) {
			t.Errorf("Store(%q) = %v, want it refused", text, err)
		}
	}
	m := Memory{Text: "the checkout retries twice", Kind: Fact, Source: "mcp"}
	if _, err := Store(db, nil, "", &m); err != nil {
		t.Fatal(err)
	}
	if _, err := Correct(db, nil, "", m.ID, &Memory{Text: "<private>it is 4417</private>", Source: "mcp"}); !errors.Is(err, ErrOnlySecret) {
		t.Errorf("Correct to a bare private span = %v, want it refused", err)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM memories WHERE text LIKE '%[private]%'`).Scan(&n)
	if n != 0 {
		t.Errorf("stored %d memory(ies) that say only [private]", n)
	}
}
