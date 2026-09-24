package selfupdate

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// Five files in this repository state the version, and a release bumped two of
// them. 0.4.6 shipped a binary whose own plugin manifest still said 0.4.5, so
// `logos doctor` warned every 0.4.6 user that their plugin was behind — with a
// fix that could not clear it, because the plugin it names was already the
// newest one published. The skew is invisible from inside any one file, which
// is exactly why nothing caught it for two releases.
//
// docs/index.html is the reference the others are measured against, for the
// reason scripts/check-release-consistency.sh gives: the release its hero
// links to is what a reader treats as the current version.
func TestEveryFileThatStatesAVersionStatesTheSameOne(t *testing.T) {
	root := filepath.Join("..", "..")
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	tag := regexp.MustCompile(`/releases/tag/v([0-9][0-9.]*)"`).FindStringSubmatch(read("docs/index.html"))
	if tag == nil {
		t.Fatal("docs/index.html links no release tag, so there is nothing to measure the others against")
	}
	want := tag[1]

	version := regexp.MustCompile(`"version"\s*:\s*"([0-9][0-9.]*)"`)
	for _, f := range []struct {
		path string
		re   *regexp.Regexp
	}{
		{"npm/package.json", version},
		{"plugin/.claude-plugin/plugin.json", version},
		{".claude-plugin/marketplace.json", version},
	} {
		m := f.re.FindStringSubmatch(read(f.path))
		if m == nil {
			t.Errorf("%s states no version, so a release cannot bump it", f.path)
			continue
		}
		if m[1] != want {
			t.Errorf("%s says %s while the site advertises %s", f.path, m[1], want)
		}
	}
}
