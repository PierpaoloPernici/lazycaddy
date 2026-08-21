package caddyfile

import "testing"

// Fuzz targets for the lossless parser and patcher. `go test` runs them
// against the seed corpus only; point Go's fuzzer at them for real
// exploration, or simply run `make fuzz`:
//
//	go test ./internal/caddyfile/ -run '^$' -fuzz FuzzParseNoPanic -fuzztime 60s
//
// Crashers land in internal/caddyfile/testdata/fuzz/<Name>/ and are
// replayed by every subsequent plain `go test`, so a regression can
// never hide once found.

// FuzzParseNoPanic asserts that Parse never panics on arbitrary input and
// that every node range stays valid against the source bytes.
func FuzzParseNoPanic(f *testing.F) {
	f.Add("example.com {\n  respond \"hi\"\n}\n")
	f.Add("a <<EOF\nbody\nEOF\n")
	f.Add("(snip) {\n  import x\n}\n")
	f.Add("&(route) {\n  respond 200\n}\n")
	f.Add("import *.conf\n")
	f.Add("{$ENV_VAR} # expansion is unmodeled\n")
	f.Add("\xef\xbb\xbfa b # c\nd {\n}")
	f.Add("a <<EOF\r\nbody\r\nEOF\r\n")
	f.Fuzz(func(t *testing.T, src string) {
		d := Parse([]byte(src))
		var walk func(ns []Node)
		walk = func(ns []Node) {
			for _, n := range ns {
				if !n.Range.Valid(len(d.Source)) {
					t.Fatalf("invalid range [%d:%d) for %d bytes, src=%q",
						n.Range.Start, n.Range.End, len(d.Source), src)
				}
				walk(n.Children)
			}
		}
		walk(d.Nodes)
	})
}

// FuzzPatchRoundTrip asserts the lossless editing contract: patching a
// parsed node's exact range changes exactly those bytes and preserves
// every byte outside it.
func FuzzPatchRoundTrip(f *testing.F) {
	f.Add("abc", 0, 1, "X")
	f.Add("hello world", 6, 11, "there")
	f.Add("example.com {\n\trespond \"hi\"\n}\n", 0, 13, "example.org {")
	f.Fuzz(func(t *testing.T, src string, start, end int, repl string) {
		d := Parse([]byte(src))
		if d.Err != nil || len(d.Nodes) == 0 {
			t.Skip()
		}
		r := d.Nodes[0].Range
		out, err := Patch([]byte(src), r, []byte(repl))
		if err != nil {
			t.Fatalf("patch: %v", err)
		}
		want := src[:r.Start] + repl + src[r.End:]
		if string(out) != want {
			t.Fatalf("patch not lossless: got %q, want %q", out, want)
		}
	})
}
