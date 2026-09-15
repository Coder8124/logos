package mcpserver

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// The browser extension is this bridge's client, and it runs inside
// chatgpt.com. Its panel was built by assigning template strings to innerHTML
// with error messages and tool descriptions spliced in, so any text from the
// bridge that ever echoed vault content would have become markup in someone
// else's page. Vault content is data; the panel has to build nodes.
func TestTheBrowserExtensionNeverBuildsMarkupFromStrings(t *testing.T) {
	scripts, err := filepath.Glob("../../extension/*.js")
	if err != nil || len(scripts) == 0 {
		t.Fatalf("no extension scripts found: %v", err)
	}
	markup := regexp.MustCompile(`\.(innerHTML|outerHTML)\s*[+]?=|insertAdjacentHTML|document\.write`)
	for _, path := range scripts {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if loc := markup.FindIndex(src); loc != nil {
			t.Errorf("%s builds markup from a string: %q", filepath.Base(path), src[loc[0]:loc[1]])
		}
	}
}
