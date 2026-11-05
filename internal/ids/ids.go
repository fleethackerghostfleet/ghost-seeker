// Package ids validates item identifiers that cross trust boundaries.
package ids

// Valid reports whether s is a safe item id: 1-64 chars of [A-Za-z0-9_-],
// so it can never escape a blob directory or carry path separators.
func Valid(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

// ValidName validates edge-supplied display identifiers (camera source, sighting_id);
// the set excludes every HTML/attribute metacharacter.
func ValidName(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '_', c == '.', c == ':', c == '/', c == '-':
		default:
			return false
		}
	}
	return true
}
