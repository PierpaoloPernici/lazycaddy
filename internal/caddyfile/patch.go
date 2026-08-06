package caddyfile

import "fmt"

// Patch returns a copy of src with the byte range r replaced by replacement.
// Every byte outside the range is preserved verbatim, which is the lossless
// editing contract: one patch changes exactly one source range.
func Patch(src []byte, r SourceRange, replacement []byte) ([]byte, error) {
	if !r.Valid(len(src)) {
		return nil, fmt.Errorf("invalid source range [%d:%d) for %d-byte source", r.Start, r.End, len(src))
	}
	out := make([]byte, 0, len(src)-(r.End-r.Start)+len(replacement))
	out = append(out, src[:r.Start]...)
	out = append(out, replacement...)
	out = append(out, src[r.End:]...)
	return out, nil
}
