package ingest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Coder8124/logos/internal/secret"
	"github.com/Coder8124/logos/internal/session"
)

// Redacting a transcript on its way into the vault.
//
// The earlier copy was byte for byte because masking a JSONL file as text is a
// way to corrupt it: one mask that lands across a JSON string boundary, or one
// <private> span that swallows a closing quote, and every line after it fails
// to parse — a corrupted archive that looks like an archive. So the edit is
// made where it is safe. A line that is JSON is decoded, every string value in
// it is masked, and the line is written back; a line that is not JSON, and any
// transcript that is not JSONL, is masked as the text it is.
//
// A line with nothing to mask is written exactly as the host wrote it. That is
// what lets a copy be compared with its source without storing anything
// beside it (the redacted stream of the source either is the copy, extends it,
// or is not it), and what makes the redactor idempotent: a copy that is
// already redacted has nothing left to change, so "is this copy still raw" is
// "would redacting it change a byte", with no marker to lose or to rebuild.

// RedactStats is what one pass masked: secrets and <private> spans apart,
// because a span the person marked is a promise kept, not a token that looked
// like a key.
type RedactStats struct {
	Secrets int
	Private int
}

func (s RedactStats) any() bool { return s.Secrets+s.Private > 0 }

func (s *RedactStats) add(found []secret.Redaction) {
	privateReason := secret.KeptPrivate("", 1)[0].Reason
	for _, r := range found {
		if r.Reason == privateReason {
			s.Private++
		} else {
			s.Secrets++
		}
	}
}

// structuralKeys hold identifiers, paths and timestamps. Their values are
// masked for known secret shapes only: the entropy check that catches a
// random-looking token would also catch a session id or a tool_use id, and a
// transcript whose ids were rewritten no longer links a result to its call.
var structuralKeys = map[string]bool{
	"id": true, "uuid": true, "sessionid": true, "session_id": true,
	"parentuuid": true, "parent_uuid": true, "tool_use_id": true, "toolu_id": true,
	"requestid": true, "request_id": true, "messageid": true, "message_id": true,
	"timestamp": true, "cwd": true, "type": true, "role": true, "model": true,
	"version": true, "gitbranch": true, "slug": true, "subtype": true, "name": true,
	"call_id": true, "turn_id": true, "originator": true,
}

// maskString masks one decoded string value.
func maskString(key, s string, st *RedactStats) string {
	out, found := secret.StripPrivate(key, s)
	st.add(found)
	if structuralKeys[strings.ToLower(key)] {
		out, found = secret.Mask(key, out)
	} else {
		out, found = secret.MaskAll(key, out)
	}
	st.add(found)
	return out
}

func encodeString(s string) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return bytes.TrimRight(b.Bytes(), "\n")
}

// rewriteValue copies the next JSON value from dec to w, masking strings. Key
// order and number spelling are kept (UseNumber), so only the strings differ.
func rewriteValue(dec *json.Decoder, w *bytes.Buffer, key string, st *RedactStats) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			w.WriteByte('{')
			first := true
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return err
				}
				k, ok := kt.(string)
				if !ok {
					return errors.New("object key is not a string")
				}
				if !first {
					w.WriteByte(',')
				}
				first = false
				w.Write(encodeString(k))
				w.WriteByte(':')
				if err := rewriteValue(dec, w, k, st); err != nil {
					return err
				}
			}
			if _, err := dec.Token(); err != nil {
				return err
			}
			w.WriteByte('}')
		case '[':
			w.WriteByte('[')
			first := true
			for dec.More() {
				if !first {
					w.WriteByte(',')
				}
				first = false
				// An array's strings are the parent key's: ["a", "b"] under
				// "cwd" is still a list of paths.
				if err := rewriteValue(dec, w, key, st); err != nil {
					return err
				}
			}
			if _, err := dec.Token(); err != nil {
				return err
			}
			w.WriteByte(']')
		}
	case string:
		w.Write(encodeString(maskString(key, t, st)))
	case json.Number:
		w.WriteString(t.String())
	case bool:
		if t {
			w.WriteString("true")
		} else {
			w.WriteString("false")
		}
	case nil:
		w.WriteString("null")
	}
	return nil
}

// redactLine masks one line, without its terminator. The input is returned
// untouched when there was nothing to mask.
func redactLine(line []byte) ([]byte, RedactStats) {
	var st RedactStats
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		dec := json.NewDecoder(bytes.NewReader(trimmed))
		dec.UseNumber()
		var out bytes.Buffer
		if err := rewriteValue(dec, &out, "", &st); err == nil {
			if _, err := dec.Token(); err == io.EOF {
				if !st.any() {
					return line, st
				}
				return out.Bytes(), st
			}
		}
		// Not (all) JSON: fall through and mask it as the text it is, from a
		// clean count.
		st = RedactStats{}
	}
	text := string(line)
	out, found := secret.StripPrivate("", text)
	st.add(found)
	out, found = secret.MaskAll("", out)
	st.add(found)
	if !st.any() {
		return line, st
	}
	return []byte(out), st
}

// redactStream writes the redacted form of r to w, a line at a time, keeping
// every line terminator, and reports what it masked and how many bytes it wrote.
func redactStream(r io.Reader, w io.Writer) (RedactStats, int64, error) {
	var (
		total RedactStats
		n     int64
	)
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			body, term := line, []byte(nil)
			if body[len(body)-1] == '\n' {
				body, term = line[:len(line)-1], line[len(line)-1:]
				if len(body) > 0 && body[len(body)-1] == '\r' {
					body, term = body[:len(body)-1], line[len(line)-2:]
				}
			}
			out, st := redactLine(body)
			total.Secrets += st.Secrets
			total.Private += st.Private
			m, werr := w.Write(out)
			n += int64(m)
			if werr != nil {
				return total, n, werr
			}
			m, werr = w.Write(term)
			n += int64(m)
			if werr != nil {
				return total, n, werr
			}
		}
		if err == io.EOF {
			return total, n, nil
		}
		if err != nil {
			return total, n, err
		}
	}
}

// redactToTemp writes the redacted form of src into a temp file beside dst and
// returns its path. It is synced like copyFile's: a rename that is durable
// before its bytes are leaves a zero-length file under the name a reader trusts.
func redactToTemp(src, dstDir, base string) (path string, st RedactStats, n int64, err error) {
	in, err := os.Open(src)
	if err != nil {
		return "", st, 0, err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(dstDir, base+".*.partial")
	if err != nil {
		return "", st, 0, err
	}
	st, n, err = redactStream(in, tmp)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp.Name())
		return "", st, 0, err
	}
	return tmp.Name(), st, n, nil
}

// heldRedacted compares an existing copy with the redacted stream of its
// source, which redactToTemp has put in tmp.
//
// A copy made while the host was mid-line ends without a newline, and that
// last fragment is masked as text where the finished line is JSON, so it can
// differ from its own completion. Only the lines before it are compared; the
// fragment is the part a later archive is meant to replace.
func heldRedacted(tmp, dst string) int {
	di, err := os.Stat(dst)
	if err != nil {
		return prefixNone
	}
	if di.Size() == 0 {
		return prefixGrown
	}
	f, err := os.Open(dst)
	if err != nil {
		return prefixDiffers
	}
	// An identical copy is the copy, whether or not it ends mid-line: read as
	// grown, it would be rewritten, and counted as archived, on every run.
	if h := heldPrefixN(tmp, dst, di.Size()); h == prefixEqual {
		return h
	}
	last := make([]byte, 1)
	_, rerr := f.ReadAt(last, di.Size()-1)
	f.Close()
	if rerr != nil {
		return prefixDiffers
	}
	if last[0] == '\n' {
		return heldPrefixN(tmp, dst, di.Size())
	}
	all, err := os.ReadFile(dst)
	if err != nil {
		return prefixDiffers
	}
	limit := int64(bytes.LastIndexByte(all, '\n') + 1)
	ti, err := os.Stat(tmp)
	if err != nil || ti.Size() <= limit {
		return prefixDiffers
	}
	if limit == 0 {
		return prefixGrown
	}
	if heldPrefixN(tmp, dst, limit) == prefixDiffers {
		return prefixDiffers
	}
	return prefixGrown
}

// extendsOnceRedacted reports whether dst, redacted as it would be today, is
// where tmp (today's redacted stream of the source) begins.
func extendsOnceRedacted(tmp, dst string) bool {
	again, _, _, err := redactToTemp(dst, filepath.Dir(dst), filepath.Base(dst))
	if err != nil {
		return false
	}
	defer os.Remove(again)
	h := heldRedacted(tmp, again)
	return h == prefixGrown || h == prefixEqual
}

// extendRedacted appends to a redacted copy only the source lines after it.
// The sweep grows a record inside resume, and redacting a 90 MB transcript from
// its first byte took three seconds of a hook's ten. A copy is a line-for-line
// image of its source, so the lines it holds are checked rather than redacted
// again: one the copy holds byte for byte had nothing to mask when it was
// kept, and one that differs must be what redacting the source line gives
// today. A raw copy passes the first test, which is why only the record path
// takes this: a harvest takes the full pass and replaces it, and doctor's
// archived-transcripts check finds one either way.
//
// false sends the caller to the full pass, which decides what the copy is: a
// copy ending mid-line, a source shorter than the copy, a line that matches
// neither way. Once it has decided, the result is final and true.
func extendRedacted(src, dst, rel string) (Archived, bool) {
	d, err := os.Open(dst)
	if err != nil {
		return Archived{}, false
	}
	defer d.Close()
	di, err := d.Stat()
	if err != nil || di.Size() == 0 {
		return Archived{}, false
	}
	last := make([]byte, 1)
	if _, err := d.ReadAt(last, di.Size()-1); err != nil || last[0] != '\n' {
		return Archived{}, false
	}
	s, err := os.Open(src)
	if err != nil {
		return Archived{}, false
	}
	defer s.Close()
	sr, dr := bufio.NewReaderSize(s, 64<<10), bufio.NewReaderSize(d, 64<<10)
	for {
		dl, derr := dr.ReadBytes('\n')
		if len(dl) == 0 && derr == io.EOF {
			break
		}
		if derr != nil {
			return Archived{}, false
		}
		sl, serr := sr.ReadBytes('\n')
		if (serr != nil && serr != io.EOF) || len(sl) == 0 || sl[len(sl)-1] != '\n' {
			return Archived{}, false
		}
		if bytes.Equal(sl, dl) {
			continue
		}
		var out bytes.Buffer
		if _, _, err := redactStream(bytes.NewReader(sl), &out); err != nil || !bytes.Equal(out.Bytes(), dl) {
			return Archived{}, false
		}
	}
	if _, err := sr.Peek(1); err == io.EOF {
		return Archived{Path: rel, Bytes: di.Size(), Unchanged: true}, true
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".*.partial")
	if err != nil {
		return refused("the copy failed", fmt.Errorf("extending the copy at %s: %w", rel, err)), true
	}
	defer os.Remove(tmp.Name())
	var st RedactStats
	var n int64
	_, err = d.Seek(0, io.SeekStart)
	if err == nil {
		_, err = io.Copy(tmp, d)
	}
	if err == nil {
		st, n, err = redactStream(sr, tmp)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), dst)
	}
	if err != nil {
		return refused("the copy failed", fmt.Errorf("extending the copy at %s: %w", rel, err)), true
	}
	return Archived{Path: rel, Bytes: di.Size() + n, Secrets: st.Secrets, Private: st.Private}, true
}

// copyIsRaw says whether redacting the file at path would change it, and how
// many secrets it holds. That is the whole test for "this archived copy is
// still raw": nothing is recorded beside the copy, so nothing can go stale.
func copyIsRaw(path string) (RedactStats, error) {
	f, err := os.Open(path)
	if err != nil {
		return RedactStats{}, err
	}
	defer f.Close()
	st, _, err := redactStream(f, io.Discard)
	return st, err
}

// ArchivedCopies lists every transcript copy in the vault, vault-relative,
// whether or not the host's own file still exists.
func ArchivedCopies(vaultDir string) ([]string, error) {
	var out []string
	root := filepath.Join(vaultDir, session.CheckpointDir)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() || filepath.Base(filepath.Dir(filepath.Dir(p))) != session.ConvosDir || !session.IsTranscriptFile(d.Name()) {
			return nil
		}
		out = append(out, vaultRel(vaultDir, p))
		return nil
	})
	return out, err
}

// RawCopy is an archived copy that still holds what redaction would mask.
type RawCopy struct {
	Path string
	RedactStats
}

// RawCopies finds the archived copies that are still raw. A copy that cannot
// be read is returned in problems, not skipped: a count that quietly leaves
// out the copy it could not open reads as a vault that is clean.
func RawCopies(vaultDir string) (raw []RawCopy, problems []string, err error) {
	copies, err := ArchivedCopies(vaultDir)
	if err != nil {
		return nil, nil, err
	}
	for _, rel := range copies {
		st, err := copyIsRaw(filepath.Join(vaultDir, filepath.FromSlash(rel)))
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", rel, err))
			continue
		}
		if st.any() {
			raw = append(raw, RawCopy{Path: rel, RedactStats: st})
		}
	}
	return raw, problems, nil
}

// RedactCopy rewrites one archived copy in place, atomically.
func RedactCopy(vaultDir, rel string) (RedactStats, error) {
	p := filepath.Join(vaultDir, filepath.FromSlash(rel))
	tmp, st, _, err := redactToTemp(p, filepath.Dir(p), filepath.Base(p))
	if err != nil {
		return st, err
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return st, err
	}
	return st, nil
}
