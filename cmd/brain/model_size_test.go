package main

import "testing"

// A custom BRAIN_EMBED was offered with the default model's size because any
// name containing "embed" matched, so someone pulling a multi-gigabyte
// embedder was told it was ~270 MB.
func TestOnlyTheDefaultEmbedderIsGivenItsSize(t *testing.T) {
	if got := modelSize("nomic-embed-text"); got != "~270 MB" {
		t.Errorf("the default embedder should keep its size, got %q", got)
	}
	if got := modelSize("nonexistent-embed"); got != "size unknown" {
		t.Errorf("another embedder was given the default's size: %q", got)
	}
}
