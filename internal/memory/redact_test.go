package memory

import (
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
