// Package selfupdate implements `brain update`: check GitHub for a newer
// release, verify it actually runs on this machine, then replace the running
// binary.
//
// This is the one place in the codebase that makes a network call on its own
// initiative rather than because a user typed a command that needs one — and
// even here, only when the user typed `brain update` specifically. It never
// runs on a schedule, from doctor, or in the background, and the request it
// makes carries no vault path, no machine identifier, no telemetry: a bare GET
// with a User-Agent naming this binary and nothing else. See Update's doc
// comment for the verification chain that runs before anything is replaced.
package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	defaultAPIBase = "https://api.github.com"
	// The repository is Coder8124/logos — the published product name — even
	// though the module and binary keep the development name, brain.
	defaultRepo = "Coder8124/logos"
)

// Asset is one file attached to a GitHub release.
type Asset struct {
	Name string
	URL  string
	Size int64
}

// Release is what the GitHub releases/latest endpoint returns, trimmed to
// what this package needs.
type Release struct {
	Version string // the tag name, e.g. "v0.3.0"
	Assets  []Asset
}

// Client talks to GitHub. Every field is overridable so tests point this at
// an httptest.Server instead of the real API — nothing in this package should
// ever need real network access to be exercised.
type Client struct {
	HTTP    *http.Client
	APIBase string
	Repo    string
	// Agent is the User-Agent sent on every request. It names the binary and
	// nothing else — no hostname, no vault path, no user identifier.
	Agent string
}

// NewClient is what `brain update` uses. version is stamped into the
// User-Agent so a look at GitHub's own request logs identifies which release
// is asking, nothing more.
func NewClient(version string) *Client {
	return &Client{
		HTTP: &http.Client{
			Timeout:       10 * time.Second,
			CheckRedirect: checkRedirect,
		},
		APIBase: defaultAPIBase,
		Repo:    defaultRepo,
		Agent:   "brain/" + version,
	}
}

// checkRedirect refuses to follow a redirect off GitHub's own hosts. Release
// assets redirect to *.githubusercontent.com; nothing else has business being
// in this chain, and a network call this narrowly scoped should not become a
// way to reach an arbitrary host via a crafted redirect.
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("too many redirects")
	}
	h := req.URL.Hostname()
	if h == "github.com" || h == "api.github.com" || strings.HasSuffix(h, ".githubusercontent.com") {
		return nil
	}
	return fmt.Errorf("refusing to follow a redirect to %s", h)
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

type ghRelease struct {
	TagName string    `json:"tag_name"`
	Assets  []ghAsset `json:"assets"`
}

// Latest fetches the newest release. The request is a bare GET: no query
// string beyond the release path, no body, no header beyond User-Agent and
// Accept.
func (c *Client) Latest() (Release, error) {
	req, err := http.NewRequest(http.MethodGet, c.APIBase+"/repos/"+c.Repo+"/releases/latest", nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("User-Agent", c.Agent)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("checking for a release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("checking for a release: unexpected status %s", resp.Status)
	}

	var gr ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&gr); err != nil {
		return Release{}, fmt.Errorf("reading the release: %w", err)
	}
	rel := Release{Version: gr.TagName}
	for _, a := range gr.Assets {
		rel.Assets = append(rel.Assets, Asset{Name: a.Name, URL: a.BrowserDownloadURL, Size: a.Size})
	}
	return rel, nil
}

// Fetch downloads one asset. Same bare-GET discipline as Latest.
func (c *Client) Fetch(url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.Agent)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading %s: unexpected status %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// FindAsset returns the asset with the given name.
func FindAsset(assets []Asset, name string) (Asset, bool) {
	for _, a := range assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// AssetName is what scripts/release.sh names one platform's archive:
// brain_<version>_<goos>_<goarch>.tar.gz, or .zip on Windows. Go's own
// GOOS/GOARCH spellings are what the script uses directly — no npm-style
// x64 translation is needed here.
func AssetName(version, goos, goarch string) string {
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("brain_%s_%s_%s.%s", version, goos, goarch, ext)
}

// VerifyChecksum checks data against the sha256 recorded for name in a
// SHA256SUMS file, in the two-column format `shasum -a 256` produces.
func VerifyChecksum(sums []byte, name string, data []byte) error {
	want := ""
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		// shasum marks binary-mode entries with a leading "*".
		if strings.TrimPrefix(fields[1], "*") == name {
			want = fields[0]
			break
		}
	}
	if want == "" {
		return fmt.Errorf("%s is not listed in SHA256SUMS", name)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", name, got, want)
	}
	return nil
}

// binaryName is what the archive holds the executable as — the development
// name, kept even inside the npm-distributed archives; see npm/bin/logos.js.
func binaryName(goos string) string {
	if goos == "windows" {
		return "brain.exe"
	}
	return "brain"
}

// ExtractBinary pulls the brain executable out of a release archive. The
// archive holds it one directory down
// (brain_<version>_<goos>_<goarch>/brain), so this matches on suffix rather
// than an exact path.
func ExtractBinary(archive []byte, goos string) ([]byte, error) {
	name := binaryName(goos)
	if goos == "windows" {
		return extractZip(archive, name)
	}
	return extractTarGz(archive, name)
}

func extractTarGz(data []byte, name string) ([]byte, error) {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("not a gzip archive: %w", err)
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag == tar.TypeReg && strings.HasSuffix(hdr.Name, "/"+name) {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("%s not found in archive", name)
}

func extractZip(data []byte, name string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("not a zip archive: %w", err)
	}
	for _, f := range zr.File {
		if f.Name == name || strings.HasSuffix(f.Name, "/"+name) {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("%s not found in archive", name)
}

// InstallKind is how this binary got onto the machine, which decides whether
// `brain update` has anything to replace.
type InstallKind int

const (
	// Standalone is a binary in an ordinary place — go install, a release
	// tarball extracted by hand, a plain reinstall. Replacing it in place is
	// exactly what the user wants.
	Standalone InstallKind = iota
	// NPX resolves a fresh copy from the registry on every invocation. There
	// is nothing durable here to replace.
	NPX
	// NPMManaged is installed through npm's per-platform package mechanism
	// (global or local). Replacing the binary works, but npm's own version
	// bookkeeping will not reflect it.
	NPMManaged
)

// DetectInstall classifies a resolved executable path. Callers should run it
// through filepath.EvalSymlinks first.
func DetectInstall(path string) InstallKind {
	norm := filepath.ToSlash(path)
	switch {
	case strings.Contains(norm, "/_npx/"), strings.Contains(norm, "_cacache/"):
		return NPX
	case strings.Contains(norm, "node_modules/@noeton/logos"):
		return NPMManaged
	default:
		return Standalone
	}
}

// IsDevBuild reports whether version is the unstamped default a plain `go
// build` produces. Updating one would silently replace a local build with a
// release binary — a footgun this refuses outright rather than warns about.
func IsDevBuild(version string) bool {
	return strings.TrimSpace(version) == "" || version == "dev"
}

// Step names which stage of Update ran or failed. A checksum mismatch and a
// binary that will not run are different sentences, and a caller should be
// able to tell a user which one happened rather than a bare "update failed".
type Step string

const (
	StepCheck    Step = "check"
	StepDownload Step = "download"
	StepChecksum Step = "checksum"
	StepVerify   Step = "verify"
	StepReplace  Step = "replace"
)

// Error names the step that failed. Every path out of Update that is not a
// clean success or "already current" returns one of these — invariant 4:
// a failure is reported, never swallowed into a success-shaped result.
type Error struct {
	Step Step
	Err  error
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %v", e.Step, e.Err) }
func (e *Error) Unwrap() error { return e.Err }

// Result is what a successful Update did, printed verbatim by the CLI. To ==
// From (both set, Asset empty) means the check ran and nothing needed doing.
type Result struct {
	From, To string
	Asset    string
	Size     int64
	Checksum string
	Replaced string
}

// Options lets tests substitute the network client, the "does this binary
// actually run" check, and how the running executable is resolved — so the
// whole flow is exercisable against an httptest.Server and a fake verify
// function, never a real GitHub API or a real exec. The zero value is what
// `brain update` uses.
type Options struct {
	Client     *Client
	Verify     func(path, wantVersion string) error
	Executable func() (string, error)
}

func (o Options) client(version string) *Client {
	if o.Client != nil {
		return o.Client
	}
	return NewClient(version)
}

func (o Options) verify() func(string, string) error {
	if o.Verify != nil {
		return o.Verify
	}
	return execVersionCheck
}

func (o Options) executable() (string, error) {
	if o.Executable != nil {
		return o.Executable()
	}
	return resolveExecutable()
}

func resolveExecutable() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}
	return p, nil
}

// execVersionCheck runs <path> version and confirms it reports wantVersion. A
// checksum proves the bytes crossed the network intact; this proves they are
// a binary that actually runs on this machine and reports the release it
// claims to be — a checksum has no opinion on whether the bytes are, say, the
// wrong architecture.
func execVersionCheck(path, wantVersion string) error {
	out, err := exec.Command(path, "version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("the new binary would not run: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), wantVersion) {
		return fmt.Errorf("the new binary reports %q, expected %s", strings.TrimSpace(string(out)), wantVersion)
	}
	return nil
}

// CheckOnly resolves the latest release without downloading or installing
// anything — `brain update --check`.
func CheckOnly(currentVersion string, opts Options) (Release, error) {
	return opts.client(currentVersion).Latest()
}

// Update is the whole flow, in the order that matters:
//
//  1. Refuse outright on an unstamped dev build.
//  2. Resolve the running executable. If it resolves to an npx cache, refuse
//     with no network call at all — there is nothing to check.
//  3. Ask GitHub what the latest release is. If it is what we already are,
//     stop here; nothing downloaded, nothing written.
//  4. Confirm the executable's directory is writable, so a permission
//     failure is reported before a byte is downloaded.
//  5. Download the archive and SHA256SUMS, and verify the archive's checksum
//     before touching anything else.
//  6. Extract the binary, write it into the executable's own directory (so
//     the final rename is same-filesystem), and run it to confirm it reports
//     the version it claims to. Only a binary that passes this is ever
//     installed.
//  7. Swap it in.
//
// Every failure names the step it failed at (see Error) rather than a bare
// "could not update", and nothing is replaced until step 6 has actually run
// the new binary — a checksum proves transport, not that the bytes work on
// this machine.
func Update(currentVersion string, opts Options) (Result, error) {
	if IsDevBuild(currentVersion) {
		return Result{}, &Error{StepCheck, fmt.Errorf(
			"this is an unstamped dev build; `brain update` would replace it with a release binary, which is almost never what you want from a build you just made")}
	}

	target, err := opts.executable()
	if err != nil {
		return Result{}, &Error{StepCheck, fmt.Errorf("could not resolve the running binary: %w", err)}
	}

	if DetectInstall(target) == NPX {
		return Result{}, &Error{StepCheck, fmt.Errorf(
			"running via npx, which resolves a fresh copy on every invocation — there is nothing here to replace. If npx has pinned an old version, try `npx clear-npx-cache`")}
	}

	client := opts.client(currentVersion)
	rel, err := client.Latest()
	if err != nil {
		return Result{}, &Error{StepCheck, err}
	}
	if rel.Version == currentVersion {
		return Result{From: currentVersion, To: rel.Version}, nil
	}

	dir := filepath.Dir(target)
	probe := filepath.Join(dir, ".brain-update-probe")
	if err := os.WriteFile(probe, []byte("x"), 0o644); err != nil {
		return Result{}, &Error{StepCheck, fmt.Errorf(
			"%s is not writable: %w — fix permissions, or run the command that owns this install (e.g. `sudo`, or `npm i -g @noeton/logos@latest`)", dir, err)}
	}
	os.Remove(probe)

	goos, goarch := runtime.GOOS, runtime.GOARCH
	assetName := AssetName(rel.Version, goos, goarch)
	asset, ok := FindAsset(rel.Assets, assetName)
	if !ok {
		return Result{}, &Error{StepDownload, fmt.Errorf("release %s has no asset named %s", rel.Version, assetName)}
	}
	sumsAsset, ok := FindAsset(rel.Assets, "SHA256SUMS")
	if !ok {
		return Result{}, &Error{StepDownload, fmt.Errorf("release %s has no SHA256SUMS", rel.Version)}
	}

	archive, err := client.Fetch(asset.URL)
	if err != nil {
		return Result{}, &Error{StepDownload, err}
	}
	sums, err := client.Fetch(sumsAsset.URL)
	if err != nil {
		return Result{}, &Error{StepDownload, err}
	}

	if err := VerifyChecksum(sums, assetName, archive); err != nil {
		return Result{}, &Error{StepChecksum, err}
	}
	sum := sha256.Sum256(archive)
	checksum := hex.EncodeToString(sum[:])

	bin, err := ExtractBinary(archive, goos)
	if err != nil {
		return Result{}, &Error{StepDownload, err}
	}

	tmp := filepath.Join(dir, ".brain-update-"+strings.TrimPrefix(rel.Version, "v"))
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		return Result{}, &Error{StepVerify, fmt.Errorf("writing the downloaded binary: %w", err)}
	}
	defer os.Remove(tmp) // no-op once the rename below has moved it into place

	if err := opts.verify()(tmp, rel.Version); err != nil {
		return Result{}, &Error{StepVerify, err}
	}

	if err := swap(tmp, target); err != nil {
		return Result{}, &Error{StepReplace, err}
	}

	return Result{
		From: currentVersion, To: rel.Version,
		Asset: assetName, Size: int64(len(archive)),
		Checksum: checksum, Replaced: target,
	}, nil
}

// swap replaces target with tmp. tmp was written into target's own directory,
// so this is a same-filesystem rename — atomic on every OS this runs on. On
// Windows the running executable cannot be overwritten directly, so the
// current one is moved aside first; everywhere else os.Rename over a running
// binary is fine, since the process holds its old inode open until it exits.
func swap(tmp, target string) error {
	if runtime.GOOS == "windows" {
		old := target + ".old"
		os.Remove(old) // best effort — a previous update's leftover, if any
		if err := os.Rename(target, old); err != nil {
			return fmt.Errorf("moving the running binary aside: %w", err)
		}
	}
	if err := os.Rename(tmp, target); err != nil {
		return fmt.Errorf("installing the new binary: %w", err)
	}
	return nil
}
