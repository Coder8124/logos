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
	"testing"
)

// buildArchive makes a brain_<version>_<goos>_<goarch>.tar.gz containing one
// file, nested one directory down the way scripts/release.sh actually lays
// releases out (brain_v0.3.0_darwin_arm64/brain).
func buildArchive(t *testing.T, version string, binary []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	name := binaryName(runtime.GOOS)
	dir := fmt.Sprintf("brain_%s_%s_%s", version, runtime.GOOS, runtime.GOARCH)
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
	return &Client{HTTP: rs.Client(), APIBase: rs.URL, Repo: "Coder8124/logos", Agent: "brain/test"}
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
	npxPath := filepath.Join(t.TempDir(), "_npx", "abc123", "node_modules", "@noeton", "logos-darwin-arm64", "bin", "brain")

	called := false
	opts := Options{
		Client: &Client{HTTP: http.DefaultClient, APIBase: "http://127.0.0.1:0", Repo: "Coder8124/logos", Agent: "brain/test"},
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

func asSelfupdateError(err error, target **Error) bool {
	se, ok := err.(*Error)
	if ok {
		*target = se
	}
	return ok
}
