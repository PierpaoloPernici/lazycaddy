package logs

// Buffer is a bounded, append-only history of log entries. Once maxLines
// is reached, appending drops the oldest entries so the tail is kept.
// It is NOT safe for concurrent use; the owner serializes access.
type Buffer struct {
	maxLines int
	entries  []Entry
}

// NewBuffer returns an empty Buffer holding at most maxLines entries
// (maxLines <= 0 uses 1000).
func NewBuffer(maxLines int) *Buffer {
	if maxLines <= 0 {
		maxLines = 1000
	}
	return &Buffer{maxLines: maxLines}
}

// Append adds entries, dropping the oldest beyond maxLines. Nil/no-op on
// empty input.
func (b *Buffer) Append(entries ...Entry) {
	if len(entries) == 0 {
		return
	}
	if len(entries) >= b.maxLines {
		b.entries = append(b.entries[:0], entries[len(entries)-b.maxLines:]...)
		return
	}
	b.entries = append(b.entries, entries...)
	if len(b.entries) > b.maxLines {
		drop := len(b.entries) - b.maxLines
		copy(b.entries, b.entries[drop:])
		b.entries = b.entries[:b.maxLines]
	}
}

// Entries returns a copy of the held entries in chronological order.
func (b *Buffer) Entries() []Entry {
	out := make([]Entry, len(b.entries))
	copy(out, b.entries)
	return out
}

// Len returns the number of held entries.
func (b *Buffer) Len() int {
	return len(b.entries)
}

// MaxLines returns the capacity.
func (b *Buffer) MaxLines() int {
	return b.maxLines
}

// Clear drops all entries.
func (b *Buffer) Clear() {
	b.entries = b.entries[:0]
}
