package setup

// standardJSON turns JSONC — JSON with // and /* */ comments and trailing
// commas, which VS Code writes into mcp.json and Cursor users type by hand —
// into JSON encoding/json accepts. hadComments reports whether anything was
// stripped that a rewrite will lose.
//
// Strings are copied untouched, so a URL's "//" is not read as a comment. A
// file that was never valid JSONC stays invalid and is refused as before.
func standardJSON(raw []byte) (out []byte, hadComments bool) {
	noComments := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case c == '"':
			j := endOfString(raw, i)
			noComments = append(noComments, raw[i:j]...)
			i = j - 1
		case c == '/' && i+1 < len(raw) && raw[i+1] == '/':
			hadComments = true
			for i < len(raw) && raw[i] != '\n' {
				i++
			}
			if i < len(raw) {
				noComments = append(noComments, '\n')
			}
		case c == '/' && i+1 < len(raw) && raw[i+1] == '*':
			hadComments = true
			i += 2
			for i+1 < len(raw) && !(raw[i] == '*' && raw[i+1] == '/') {
				i++
			}
			i++ // onto the closing '/'
			noComments = append(noComments, ' ')
		default:
			noComments = append(noComments, c)
		}
	}

	out = make([]byte, 0, len(noComments))
	for i := 0; i < len(noComments); i++ {
		c := noComments[i]
		if c == '"' {
			j := endOfString(noComments, i)
			out = append(out, noComments[i:j]...)
			i = j - 1
			continue
		}
		if c == ',' {
			j := i + 1
			for j < len(noComments) && isSpace(noComments[j]) {
				j++
			}
			if j < len(noComments) && (noComments[j] == '}' || noComments[j] == ']') {
				continue
			}
		}
		out = append(out, c)
	}
	return out, hadComments
}

// endOfString returns the index just past the string literal opening at i.
func endOfString(b []byte, i int) int {
	for j := i + 1; j < len(b); j++ {
		switch b[j] {
		case '\\':
			j++
		case '"':
			return j + 1
		}
	}
	return len(b)
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}
