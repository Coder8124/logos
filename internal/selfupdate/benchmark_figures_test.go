package selfupdate

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// The headline benchmark result is typed by hand into four places, and they
// drifted: the README, SETUP and the site said logos passes 84.4% while the
// benchmark page those three link to as the source still showed an older
// run's 81.2%. A reader who checked the claim found a different number at
// its source. As with the version, no single file can see the skew.
//
// The benchmark page's results table is the reference: it is the source the
// others cite.
func TestEveryPageThatQuotesTheBenchmarkQuotesTheSameLogosResult(t *testing.T) {
	root := filepath.Join("..", "..")
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		return string(b)
	}
	row := regexp.MustCompile(`(?m)^logos\s+(\d+\.\d)%`)

	ref := row.FindStringSubmatch(read("docs/continuity-benchmark.md"))
	if ref == nil {
		t.Fatal("docs/continuity-benchmark.md has no logos row in its results table")
	}
	want := ref[1]

	checks := []struct {
		file string
		re   *regexp.Regexp
	}{
		{"README.md", regexp.MustCompile(`Logos passes \*\*(\d+\.\d)%\*\*`)},
		{"SETUP.md", regexp.MustCompile(`Logos passes \*\*(\d+\.\d)%\*\*`)},
		{"SETUP.md", row},
		{"docs/index.html", regexp.MustCompile(`<b>(\d+\.\d)%</b><span>handoff pass rate`)},
	}
	for _, c := range checks {
		ms := c.re.FindAllStringSubmatch(read(c.file), -1)
		if len(ms) == 0 {
			t.Errorf("%s no longer quotes the logos result as %s; update this test to where it moved", c.file, c.re)
			continue
		}
		for _, m := range ms {
			if m[1] != want {
				t.Errorf("%s says logos passes %s%%, but the benchmark page it cites says %s%%", c.file, m[1], want)
			}
		}
	}
}
