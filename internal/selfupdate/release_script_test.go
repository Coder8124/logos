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

// A 0.4 install updates itself by asking the release for
// brain_<version>_<os>_<arch>.tar.gz, checking it against SHA256SUMS, and
// running the file inside whose path ends in /brain. After the rename the
// release carried only logos_ archives, so every 0.4 `brain update` failed
// with "no asset" and stayed on 0.4 for good. This runs the real release
// script for this machine's platform and opens its output the way 0.4 does.
func TestAReleaseStillCarriesTheArchiveA04UpdateAsksFor(t *testing.T) {
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
	version := "v0.4.99"
	cmd := exec.Command("bash", filepath.Join("..", "..", "scripts", "release.sh"), version)
	cmd.Env = append(os.Environ(),
		"RELEASE_OUT="+out,
		"RELEASE_PLATFORMS="+runtime.GOOS+" "+runtime.GOARCH,
	)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("release.sh: %v\n%s", err, b)
	}

	for _, prefix := range []string{"logos", "brain"} {
		if _, err := os.Stat(filepath.Join(out, fmt.Sprintf("%s_%s_%s_%s.tar.gz", prefix, version, runtime.GOOS, runtime.GOARCH))); err != nil {
			t.Errorf("the release has no %s archive: %v", prefix, err)
		}
	}

	// The name 0.4's AssetName built, spelled out rather than borrowed from
	// this package, which now names logos_ archives.
	name := fmt.Sprintf("brain_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
	archive, err := os.ReadFile(filepath.Join(out, name))
	if err != nil {
		t.Fatal(err)
	}
	sums, err := os.ReadFile(filepath.Join(out, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyChecksum(sums, name, archive); err != nil {
		t.Fatalf("0.4 could not verify %s: %v", name, err)
	}

	bin := fileEndingIn(t, archive, "/brain")
	path := filepath.Join(t.TempDir(), "brain")
	if err := os.WriteFile(path, bin, 0o755); err != nil {
		t.Fatal(err)
	}
	// 0.4 refuses to swap in a binary whose `version` does not name the release.
	got, err := exec.Command(path, "version").CombinedOutput()
	if err != nil || !strings.Contains(string(got), version) {
		t.Errorf("the binary 0.4 would install reports %q (%v), want %s", got, err, version)
	}
}

// fileEndingIn is 0.4's extractTarGz: the first regular file whose path ends
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
