package main

import (
	"os"
	"path/filepath"
	"testing"
)

func deleteIndex(t *testing.T) {
	t.Helper()
	for _, f := range []string{"index.db", "index.db-wal", "index.db-shm"} {
		if err := os.Remove(filepath.Join(vaultPath(), ".logos", f)); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
}

func TestRecallOnADeletedIndexIsNotASQLError(t *testing.T) {
	standIn(t, "shop")
	deleteIndex(t)
	captureStdout(t, func() {
		if err := recallCmd([]string{"postgres"}); err != nil {
			t.Errorf("recall on a deleted index failed: %v", err)
		}
	})
}
