package selfupdate

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// The download table on the site is hand-edited at each release, and after the
// rename it still linked brain_ archives — which a release keeps publishing for
// 0.4 updaters, so the links worked and quietly handed a new user the binary
// under the old name. Each link must name the archive AssetName builds.
func TestTheSiteDownloadLinksNameTheArchivesAReleasePublishes(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "..", "docs", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	link := regexp.MustCompile(`releases/download/(v[^/"]+)/([^"]+)"`)
	matches := link.FindAllStringSubmatch(string(page), -1)
	if len(matches) == 0 {
		t.Fatal("found no release download links in docs/index.html")
	}

	platforms := [][2]string{
		{"darwin", "arm64"}, {"darwin", "amd64"},
		{"linux", "amd64"}, {"linux", "arm64"},
		{"windows", "amd64"},
	}
	for _, m := range matches {
		tag, file := m[1], m[2]
		if file == "SHA256SUMS" {
			continue
		}
		ok := false
		for _, p := range platforms {
			if file == AssetName(tag, p[0], p[1]) {
				ok = true
			}
		}
		if !ok {
			t.Errorf("the site links %s for %s, which is not an archive AssetName names", file, tag)
		}
	}
}
