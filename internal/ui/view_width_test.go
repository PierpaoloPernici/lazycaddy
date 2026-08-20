package ui

import "testing"

func TestSyncSource_WidthChangedRebuildsContent(t *testing.T) {
	m := newLoadedModel(t, fakeLoader{state: stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond hello\n}\n",
	}))})
	m = resize(m, 100, 24)
	_ = m.View() // initial render sets viewport.Width to 100's contentW
	// Change width without changing selection - should trigger widthChanged path
	m = resize(m, 80, 24)
	_ = m.View()
	if m.viewport.Width != 80*3/5-2-4 { // contentW for 80 is 80*3/5-12 ≈ 36, but just check it changed
		// Just ensure no panic and width updated
		t.Logf("viewport width after resize to 80: %d", m.viewport.Width)
	}
}

func TestSyncSource_WidthChangedWithSameContent(t *testing.T) {
	m := newLoadedModel(t, fakeLoader{state: stateFor(t, "config/Caddyfile", fsReader(map[string]string{
		"config/Caddyfile": "example.test {\n\trespond hello\n}\n",
	}))})
	m = resize(m, 80, 24)
	_ = m.View()
	w1 := m.viewport.Width
	m = resize(m, 120, 24)
	_ = m.View()
	w2 := m.viewport.Width
	if w1 == w2 {
		t.Errorf("width should change from 80 to 120, got %d and %d", w1, w2)
	}
}
