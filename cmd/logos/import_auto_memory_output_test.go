package main

import (
	"strings"
	"testing"
)

func TestAnEmptyAutoMemoryImportSaysMemoriesNotMemorys(t *testing.T) {
	standInAutoMemory(t, nil)
	out := importRules(t, claudeAutoMemorySource)
	if !strings.Contains(out, "no claude-auto-memory memories in") {
		t.Errorf("the empty import does not say memories:\n%s", out)
	}
}
