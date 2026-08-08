package logs

import (
	"testing"
)

func entriesOf(ss ...string) []Entry {
	out := make([]Entry, len(ss))
	for i, s := range ss {
		out[i] = Entry{Raw: []byte(s)}
	}
	return out
}

func rawStrings(entries []Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = string(e.Raw)
	}
	return out
}

func TestBuffer_AppendUnderCapacity(t *testing.T) {
	b := NewBuffer(5)
	b.Append(entriesOf("a", "b", "c")...)
	if b.Len() != 3 {
		t.Fatalf("Len = %d, want 3", b.Len())
	}
	if got, want := rawStrings(b.Entries()), []string{"a", "b", "c"}; !equalStrings(got, want) {
		t.Errorf("Entries = %v, want %v", got, want)
	}
}

func TestBuffer_AppendExactlyCapacity(t *testing.T) {
	b := NewBuffer(3)
	b.Append(entriesOf("a", "b", "c")...)
	if b.Len() != 3 {
		t.Fatalf("Len = %d, want 3", b.Len())
	}
	if got, want := rawStrings(b.Entries()), []string{"a", "b", "c"}; !equalStrings(got, want) {
		t.Errorf("Entries = %v, want %v", got, want)
	}
}

func TestBuffer_AppendOverCapacity(t *testing.T) {
	b := NewBuffer(3)
	b.Append(entriesOf("a", "b", "c", "d", "e")...)
	if b.Len() != 3 {
		t.Fatalf("Len = %d, want 3", b.Len())
	}
	if got, want := rawStrings(b.Entries()), []string{"c", "d", "e"}; !equalStrings(got, want) {
		t.Errorf("Entries = %v, want %v (oldest dropped, tail kept)", got, want)
	}
}

func TestBuffer_AppendBatched(t *testing.T) {
	b := NewBuffer(3)
	b.Append(entriesOf("a", "b")...)
	b.Append(entriesOf("c", "d")...)
	if got, want := rawStrings(b.Entries()), []string{"b", "c", "d"}; !equalStrings(got, want) {
		t.Errorf("Entries = %v, want %v", got, want)
	}
}

func TestBuffer_AppendEmpty(t *testing.T) {
	b := NewBuffer(3)
	b.Append()
	b.Append(nil...)
	if b.Len() != 0 {
		t.Errorf("Len = %d, want 0", b.Len())
	}
}

func TestBuffer_MaxLinesDefault(t *testing.T) {
	if got := NewBuffer(0).MaxLines(); got != 1000 {
		t.Errorf("MaxLines = %d, want 1000", got)
	}
	if got := NewBuffer(-5).MaxLines(); got != 1000 {
		t.Errorf("MaxLines = %d, want 1000", got)
	}
	if got := NewBuffer(42).MaxLines(); got != 42 {
		t.Errorf("MaxLines = %d, want 42", got)
	}
}

func TestBuffer_Clear(t *testing.T) {
	b := NewBuffer(3)
	b.Append(entriesOf("a", "b", "c")...)
	b.Clear()
	if b.Len() != 0 {
		t.Errorf("Len = %d, want 0 after Clear", b.Len())
	}
	if got := b.Entries(); len(got) != 0 {
		t.Errorf("Entries = %v, want empty", got)
	}
	// The buffer remains usable after Clear.
	b.Append(entriesOf("a", "b")...)
	if b.Len() != 2 {
		t.Errorf("Len = %d, want 2 after reusing cleared buffer", b.Len())
	}
}

func TestBuffer_EntriesReturnsCopy(t *testing.T) {
	b := NewBuffer(5)
	b.Append(entriesOf("a", "b")...)

	got := b.Entries()
	got[0] = Entry{Raw: []byte("Z")}
	got = append(got, Entry{Raw: []byte("extra")})

	if b.Len() != 2 {
		t.Errorf("buffer Len = %d, want 2 (mutating returned slice leaked)", b.Len())
	}
	if raw := rawStrings(b.Entries()); !equalStrings(raw, []string{"a", "b"}) {
		t.Errorf("buffer Entries = %v, want [a b]", raw)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
