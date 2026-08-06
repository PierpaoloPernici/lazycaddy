package caddyfile

// SourceRange identifies an exact byte range in a source document. It is the
// smallest unit a patch may replace; every byte outside the range is
// preserved verbatim.
type SourceRange struct {
	// Start is the byte offset of the first byte in the range, inclusive.
	Start int
	// End is the byte offset one past the last byte in the range, exclusive.
	End int
	// StartLine is the 1-based line number of Start.
	StartLine int
	// EndLine is the 1-based line number of the last byte in the range
	// (the line containing End-1).
	EndLine int
}

// Text returns the exact source bytes covered by the range.
func (r SourceRange) Text(src []byte) string {
	return string(src[r.Start:r.End])
}

// Valid reports whether the range is well-formed for a source of the given
// length.
func (r SourceRange) Valid(srcLen int) bool {
	return r.Start >= 0 && r.Start <= r.End && r.End <= srcLen
}
