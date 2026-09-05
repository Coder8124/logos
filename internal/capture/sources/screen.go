package sources

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Screen capture: a screenshot, then on-device OCR through the Vision
// framework, reached via JXA under osascript — the same external-process,
// no-cgo path the calendar source uses. Nothing here touches the network; the
// OCR is local.
//
// This is the most invasive capability in the system, which is why it is a
// standalone primitive rather than wired into the capture daemon: nothing
// under cmd/ or app/ calls CaptureScreenText, so there is currently no flag a
// user can set to turn it on. A caller that adds one must gate it the way the
// rest of capture does — off by default, explicit opt-in, degrading to
// "unavailable" on a missing permission rather than erroring.

// IdleSeconds reports how long since the last HID (keyboard/mouse) event.
//
// Read from IOKit via ioreg — no permission required, unlike screen capture.
// This is what powers the tutor's "you've been staring at this, need help?"
// prompt: a long idle stretch while a studious page is up reads as someone
// stuck on a problem.
func IdleSeconds() (float64, error) {
	out, err := exec.Command("sh", "-c",
		`ioreg -c IOHIDSystem | awk '/HIDIdleTime/ {print $NF; exit}'`).Output()
	if err != nil {
		return 0, err
	}
	ns, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return 0, fmt.Errorf("unexpected HIDIdleTime %q", strings.TrimSpace(string(out)))
	}
	return ns / 1e9, nil // HIDIdleTime is in nanoseconds
}

// visionOCR runs Apple's Vision text recogniser over an image file and returns
// the recognised text. Accuracy level "accurate" over "fast" — a study page is
// dense and we run this rarely, so latency is a fair trade for fewer errors.
const visionOCRScript = `(function() {
  ObjC.import('Vision');
  ObjC.import('Foundation');
  ObjC.import('AppKit');
  const url = $.NSURL.fileURLWithPath('IMGPATH');
  const img = $.NSImage.alloc.initWithContentsOfURL(url);
  if (!img) return '';
  const cg = img.CGImageForProposedRectContextHints($(), $(), $());
  const handler = $.VNImageRequestHandler.alloc.initWithCGImageOptions(cg, $());
  const req = $.VNRecognizeTextRequest.alloc.init;
  req.recognitionLevel = 1; // accurate
  req.usesLanguageCorrection = true;
  handler.performRequestsError($([req]), $());
  const results = req.results;
  const lines = [];
  for (let i = 0; i < results.count; i++) {
    const top = results.objectAtIndex(i).topCandidates(1);
    if (top.count > 0) lines.push(ObjC.unwrap(top.objectAtIndex(0).string));
  }
  return lines.join('\n');
})()`

// CaptureScreenText grabs the main display and OCRs it, returning the text.
//
// Two permissions gate this: Screen Recording (for screencapture) and, on
// first Vision use, nothing extra. Either failure returns an error the caller
// treats as "screen notes unavailable" and moves on.
func CaptureScreenText(scratch string) (string, error) {
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return "", err
	}
	shot := scratch + "/screen.png"
	defer os.Remove(shot)

	// -x silences the capture sound; -o omits the window shadow. No -i, so this
	// is non-interactive.
	cap := exec.Command("screencapture", "-x", "-o", shot)
	if err := cap.Run(); err != nil {
		return "", fmt.Errorf("screencapture failed (Screen Recording permission?): %w", err)
	}
	if fi, err := os.Stat(shot); err != nil || fi.Size() == 0 {
		return "", fmt.Errorf("screenshot was empty (Screen Recording likely denied)")
	}

	script := strings.Replace(visionOCRScript, "IMGPATH", shot, 1)
	out, err := exec.Command("osascript", "-l", "JavaScript", "-e", script).Output()
	if err != nil {
		return "", fmt.Errorf("vision OCR failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
