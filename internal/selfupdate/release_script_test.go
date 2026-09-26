package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// logos update asks the release for AssetName's archive, checks it against
// SHA256SUMS, and swaps in the file inside whose path ends in /logos, refusing
// one whose version does not name the release. This runs the real release
// script for this machine's platform and opens its output the same way. Since
// 0.5.0 the release carries no brain_ archive: only 0.4.0 to 0.4.2 asked for
// one, and a second copy of every binary was the price of keeping them.
func TestAReleaseCarriesTheArchiveLogosUpdateAsksForAndNoOther(t *testing.T) {
	if testing.Short() {
		t.Skip("cross-compiles the CLI")
	}
	if runtime.GOOS == "windows" {
		t.Skip("release.sh is a bash script and the archive here is a tarball")
	}
	for _, tool := range []string{"bash", "shasum", "tar"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("no %s", tool)
		}
	}

	out := t.TempDir()
	version := "v0.5.99"
	cmd := exec.Command("bash", filepath.Join("..", "..", "scripts", "release.sh"), version)
	cmd.Env = append(os.Environ(),
		"RELEASE_OUT="+out,
		"RELEASE_PLATFORMS="+runtime.GOOS+" "+runtime.GOARCH,
	)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("release.sh: %v\n%s", err, b)
	}

	old := fmt.Sprintf("brain_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
	if _, err := os.Stat(filepath.Join(out, old)); err == nil {
		t.Errorf("the release still carries %s", old)
	}

	name := AssetName(version, runtime.GOOS, runtime.GOARCH)
	archive, err := os.ReadFile(filepath.Join(out, name))
	if err != nil {
		t.Fatal(err)
	}
	sums, err := os.ReadFile(filepath.Join(out, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyChecksum(sums, name, archive); err != nil {
		t.Fatalf("update could not verify %s: %v", name, err)
	}

	bin := fileEndingIn(t, archive, "/logos")
	path := filepath.Join(t.TempDir(), "logos")
	if err := os.WriteFile(path, bin, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := exec.Command(path, "version").CombinedOutput()
	if err != nil || !strings.Contains(string(got), version) {
		t.Errorf("the binary update would install reports %q (%v), want %s", got, err, version)
	}
}

// fileEndingIn is update's extraction: the first regular file whose path ends
// in suffix.
func fileEndingIn(t *testing.T, archive []byte, suffix string) []byte {
	t.Helper()
	gr, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			t.Fatalf("no file ending in %s in the archive", suffix)
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag == tar.TypeReg && strings.HasSuffix(hdr.Name, suffix) {
			b, err := io.ReadAll(tr)
			if err != nil {
				t.Fatal(err)
			}
			return b
		}
	}
}
