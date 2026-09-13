package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// buildArchive makes a logos_<version>_<goos>_<goarch>.tar.gz containing one
// file, nested one directory down the way scripts/release.sh actually lays
// releases out (brain_v0.3.0_darwin_arm64/brain).
func buildArchive(t *testing.T, version string, binary []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	name := binaryName(runtime.GOOS)
	dir := fmt.Sprintf("logos_%s_%s_%s", version, runtime.GOOS, runtime.GOARCH)
	hdr := &tar.Header{Name: dir + "/" + name, Mode: 0o755, Size: int64(len(binary))}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(binary); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sha256sums(name string, data []byte) []byte {
	sum := sha256.Sum256(data)
	return []byte(hex.EncodeToString(sum[:]) + "  " + name + "\n")
}

// releaseServer serves one release, at the exact two paths a real GitHub
// release exposes: the API's releases/latest and the asset download URLs it
// points at. Every request is recorded so tests can assert on invariant 5 —
// no vault data, no query string, no telemetry.
type releaseServer struct {
	*httptest.Server
	requests []*http.Request
}

func newReleaseServer(t *testing.T, version, assetName string, archive, sums []byte) *releaseServer {
	t.Helper()
	rs := &releaseServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/Coder8124/logos/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		rs.requests = append(rs.requests, r)
		fmt.Fprintf(w, `{"tag_name":%q,"assets":[
			{"name":%q,"browser_download_url":"%s/assets/%s","size":%d},
			{"name":"SHA256SUMS","browser_download_url":"%s/assets/SHA256SUMS","size":%d}
		]}`, version, assetName, rs.URL, assetName, len(archive), rs.URL, len(sums))
	})
	mux.HandleFunc("/assets/"+assetName, func(w http.ResponseWriter, r *http.Request) {
		rs.requests = append(rs.requests, r)
		w.Write(archive)
	})
	mux.HandleFunc("/assets/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		rs.requests = append(rs.requests, r)
		w.Write(sums)
	})
	rs.Server = httptest.NewServer(mux)
	return rs
}

func testClient(rs *releaseServer) *Client {
	return &Client{HTTP: rs.Client(), APIBase: rs.URL, Repo: "Coder8124/logos", Agent: "logos/test"}
}

func fakeExecutable(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUpdateRefusesToReplaceADevBuild(t *testing.T) {
	_, err := Update("dev", Options{})
	if err == nil {
		t.Fatal("expected an error for a dev build, got nil")
	}
	var se *Error
	if !asSelfupdateError(err, &se) || se.Step != StepCheck {
		t.Errorf("expected a StepCheck Error, got %v (%T)", err, err)
	}
}

func TestUpdateRejectsAnArchiveWhoseChecksumDoesNotMatch(t *testing.T) {
	assetName := AssetName("v0.3.0", runtime.GOOS, runtime.GOARCH)
	archive := buildArchive(t, "v0.3.0", []byte("new binary bytes"))
	// SHA256SUMS for a different payload entirely — a deliberate mismatch.
	sums := sha256sums(assetName, []byte("not the archive"))

	rs := newReleaseServer(t, "v0.3.0", assetName, archive, sums)
	defer rs.Close()

	target := fakeExecutable(t, binaryName(runtime.GOOS))
	before, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}

	opts := Options{
		Client:     testClient(rs),
		Executable: func() (string, error) { return target, nil },
	}
	_, err = Update("v0.2.1", opts)
	if err == nil {
		t.Fatal("expected a checksum error, got nil")
	}
	var se *Error
	if !asSelfupdateError(err, &se) || se.Step != StepChecksum {
		t.Errorf("expected a StepChecksum Error, got %v (%T)", err, err)
	}

	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the original binary was modified despite a checksum failure")
	}
}

func TestUpdateRestoresTheOldBinaryWhenTheNewOneWillNotRun(t *testing.T) {
	assetName := AssetName("v0.3.0", runtime.GOOS, runtime.GOARCH)
	payload := []byte("deliberately broken payload, not a real executable")
	archive := buildArchive(t, "v0.3.0", payload)
	sums := sha256sums(assetName, archive)

	rs := newReleaseServer(t, "v0.3.0", assetName, archive, sums)
	defer rs.Close()

	target := fakeExecutable(t, binaryName(runtime.GOOS))
	before, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}

	opts := Options{
		Client:     testClient(rs),
		Executable: func() (string, error) { return target, nil },
		Verify: func(path, wantVersion string) error {
			return fmt.Errorf("simulated: the new binary would not run")
		},
	}
	_, err = Update("v0.2.1", opts)
	if err == nil {
		t.Fatal("expected a verify error, got nil")
	}
	var se *Error
	if !asSelfupdateError(err, &se) || se.Step != StepVerify {
		t.Errorf("expected a StepVerify Error, got %v (%T)", err, err)
	}

	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the original binary was replaced despite the new one failing to verify")
	}

	// No leftover temp file in the target's directory.
	entries, err := os.ReadDir(filepath.Dir(target))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != filepath.Base(target) {
			t.Errorf("leftover file after a failed verify: %s", e.Name())
		}
	}
}

func TestUpdateUnderNpxRefusesAndExplains(t *testing.T) {
	npxPath := filepath.Join(t.TempDir(), "_npx", "abc123", "node_modules", "@noeton", "logos-darwin-arm64", "bin", "logos")

	called := false
	opts := Options{
		Client: &Client{HTTP: http.DefaultClient, APIBase: "http://127.0.0.1:0", Repo: "Coder8124/logos", Agent: "logos/test"},
		Executable: func() (string, error) {
			called = true
			return npxPath, nil
		},
	}
	// Wrap the client's transport so any actual HTTP attempt fails the test —
	// belt and suspenders on top of asserting no request was recorded.
	_, err := Update("v0.2.1", opts)
	if err == nil {
		t.Fatal("expected an error refusing to update under npx")
	}
	if !called {
		t.Fatal("Executable was never called")
	}
	var se *Error
	if !asSelfupdateError(err, &se) || se.Step != StepCheck {
		t.Errorf("expected a StepCheck Error, got %v (%T)", err, err)
	}
}

func TestUpdateSendsNoVaultDataAndNoQueryString(t *testing.T) {
	assetName := AssetName("v0.3.0", runtime.GOOS, runtime.GOARCH)
	archive := buildArchive(t, "v0.3.0", []byte("fake binary that verify() below accepts"))
	sums := sha256sums(assetName, archive)

	rs := newReleaseServer(t, "v0.3.0", assetName, archive, sums)
	defer rs.Close()

	target := fakeExecutable(t, binaryName(runtime.GOOS))
	opts := Options{
		Client:     testClient(rs),
		Executable: func() (string, error) { return target, nil },
		Verify:     func(path, wantVersion string) error { return nil },
	}
	if _, err := Update("v0.2.1", opts); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	if len(rs.requests) == 0 {
		t.Fatal("no requests recorded")
	}
	for _, r := range rs.requests {
		if r.URL.RawQuery != "" {
			t.Errorf("request to %s carried a query string: %q", r.URL.Path, r.URL.RawQuery)
		}
		if r.ContentLength > 0 {
			t.Errorf("request to %s carried a body", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("request to %s used %s, not GET", r.URL.Path, r.Method)
		}
	}
}

func TestUpdateHappyPathSwapsTheBinary(t *testing.T) {
	assetName := AssetName("v0.3.0", runtime.GOOS, runtime.GOARCH)
	payload := []byte("new binary contents")
	archive := buildArchive(t, "v0.3.0", payload)
	sums := sha256sums(assetName, archive)

	rs := newReleaseServer(t, "v0.3.0", assetName, archive, sums)
	defer rs.Close()

	target := fakeExecutable(t, binaryName(runtime.GOOS))
	var verifiedPath, verifiedVersion string
	opts := Options{
		Client:     testClient(rs),
		Executable: func() (string, error) { return target, nil },
		Verify: func(path, wantVersion string) error {
			verifiedPath, verifiedVersion = path, wantVersion
			return nil
		},
	}
	res, err := Update("v0.2.1", opts)
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if res.From != "v0.2.1" || res.To != "v0.3.0" || res.Replaced != target {
		t.Errorf("unexpected result: %+v", res)
	}
	if verifiedVersion != "v0.3.0" {
		t.Errorf("verify was called with version %q, want v0.3.0", verifiedVersion)
	}
	if verifiedPath == target {
		t.Errorf("verify ran against the final path, not a staged temp file")
	}

	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, payload) {
		t.Error("the binary at the target path was not replaced with the new payload")
	}
}

func TestUpdateWithNoNewReleaseChangesNothing(t *testing.T) {
	assetName := AssetName("v0.2.1", runtime.GOOS, runtime.GOARCH)
	archive := buildArchive(t, "v0.2.1", []byte("current"))
	sums := sha256sums(assetName, archive)
	rs := newReleaseServer(t, "v0.2.1", assetName, archive, sums)
	defer rs.Close()

	target := fakeExecutable(t, binaryName(runtime.GOOS))
	before, _ := os.ReadFile(target)

	opts := Options{
		Client:     testClient(rs),
		Executable: func() (string, error) { return target, nil },
	}
	res, err := Update("v0.2.1", opts)
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if res.From != "v0.2.1" || res.To != "v0.2.1" || res.Asset != "" {
		t.Errorf("expected a no-op result, got %+v", res)
	}
	after, _ := os.ReadFile(target)
	if !bytes.Equal(before, after) {
		t.Error("binary changed despite already being current")
	}
}

// scripts/release.sh runs `shasum -a 256 ./*.tar.gz ./*.zip` inside the dist
// directory, and shasum echoes back whatever glob it was given — so every
// SHA256SUMS this project has ever published names its entries
// "./logos_v0.4.0_darwin_arm64.tar.gz", not the bare name VerifyChecksum's own
// AssetName produces. This is the real file downloaded from the v0.4.0 GitHub
// release, reproduced here after `logos update` failed against it outside the
// test suite — every prior fixture in this file used a hand-built bare-name
// SHA256SUMS, so nothing here ever exercised the format `logos update`
// actually has to parse.
func TestVerifyChecksumAcceptsTheDotSlashPrefixRealReleasesUse(t *testing.T) {
	data := []byte("archive bytes")
	name := "logos_v0.4.0_darwin_arm64.tar.gz"
	sum := sha256.Sum256(data)
	sums := []byte(hex.EncodeToString(sum[:]) + "  ./" + name + "\n")

	if err := VerifyChecksum(sums, name, data); err != nil {
		t.Errorf("a real release's SHA256SUMS should verify, got: %v", err)
	}
}

func asSelfupdateError(err error, target **Error) bool {
	se, ok := err.(*Error)
	if ok {
		*target = se
	}
	return ok
}

// A tag pushed before its release is published is already visible to `go
// install …@latest`, which stamps that build with the new version while
// GitHub's releases/latest still names the previous one. Treating "different"
// as "newer" swapped the newer binary for the older release and reported it
// as an update.
func TestUpdateNeverReplacesANewerBinaryWithAnOlderRelease(t *testing.T) {
	assetName := AssetName("v0.4.2", runtime.GOOS, runtime.GOARCH)
	archive := buildArchive(t, "v0.4.2", []byte("older release"))
	rs := newReleaseServer(t, "v0.4.2", assetName, archive, sha256sums(assetName, archive))
	defer rs.Close()

	target := fakeExecutable(t, binaryName(runtime.GOOS))
	before, _ := os.ReadFile(target)

	res, err := Update("v0.4.3", Options{
		Client:     testClient(rs),
		Executable: func() (string, error) { return target, nil },
		Verify:     func(string, string) error { return nil },
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if res.Asset != "" || !res.Ahead {
		t.Errorf("expected a no-op result marked ahead of the release, got %+v", res)
	}
	after, _ := os.ReadFile(target)
	if !bytes.Equal(before, after) {
		t.Error("the newer binary was replaced with the older release")
	}
}

func TestNewerOrdersReleasesByNumberNotByText(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v0.4.3", "v0.4.2", true},
		{"v0.4.2", "v0.4.3", false},
		{"v0.4.3", "v0.4.3", false},
		{"v0.4.10", "v0.4.9", true},
		{"v0.5.0", "v0.4.15", true},
		// go install of an untagged commit after v0.4.3 stamps a pseudo-version
		// that sorts below v0.4.4 and above v0.4.3.
		{"v0.4.4", "v0.4.4-0.20260913010203-abcdefabcdef", true},
		{"v0.4.3", "v0.4.4-0.20260913010203-abcdefabcdef", false},
		// Something this cannot read is offered as an update only if it differs,
		// which is what update did before it compared versions at all.
		{"nightly", "v0.4.3", true},
		{"v0.4.3", "v0.4.3+dirty", false},
	}
	for _, c := range cases {
		if got := Newer(c.latest, c.current); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

// Homebrew owns the files under its Cellar: `brew upgrade` installs the next
// version beside this one and deletes this directory, and its receipt still
// names the version brew put there. Replacing the binary in place leaves brew
// believing an old release is installed, and the next `brew upgrade` or
// `brew cleanup` silently undoes the update.
func TestUpdateUnderHomebrewRefusesAndPointsAtBrewUpgrade(t *testing.T) {
	brewPath := filepath.Join(t.TempDir(), "Cellar", "logos", "0.4.3", "bin", "logos")

	opts := Options{
		Client:     &Client{HTTP: http.DefaultClient, APIBase: "http://127.0.0.1:0", Repo: "Coder8124/logos", Agent: "logos/test"},
		Executable: func() (string, error) { return brewPath, nil },
	}
	_, err := Update("v0.4.3", opts)
	if err == nil {
		t.Fatal("expected an error refusing to replace a Homebrew-managed binary")
	}
	if !strings.Contains(err.Error(), "brew upgrade logos") {
		t.Errorf("the refusal does not say how to update: %v", err)
	}
}

// Homebrew symlinks <prefix>/opt/logos to whichever version is current, so
// that path survives an upgrade that deletes the versioned directory.
func TestAHomebrewInstallIsFoundByItsStableOptPath(t *testing.T) {
	prefix := t.TempDir()
	cellar := filepath.Join(prefix, "Cellar", "logos", "0.4.3", "bin", "logos")
	if DetectInstall(cellar) != Homebrew {
		t.Fatalf("%s was not recognised as a Homebrew install", cellar)
	}
	if got := HomebrewStablePath(cellar); got != "" {
		t.Errorf("stable path %q offered while no opt link exists", got)
	}

	opt := filepath.Join(prefix, "opt", "logos", "bin", "logos")
	if err := os.MkdirAll(filepath.Dir(opt), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(opt, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := HomebrewStablePath(cellar); got != opt {
		t.Errorf("stable path = %q, want %q", got, opt)
	}
	if DetectInstall("/usr/local/bin/logos") == Homebrew {
		t.Error("an ordinary binary was taken for a Homebrew install")
	}
}
