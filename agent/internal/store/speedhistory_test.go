package store

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
)

func TestSpeedHistory(t *testing.T) {
	h := OpenSpeedHistory(filepath.Join(t.TempDir(), "h.json"))
	if got := h.List(); len(got) != 0 {
		t.Fatalf("empty list: %v", got)
	}
	for i := 0; i < speedHistoryMax+5; i++ {
		if _, err := h.Add(json.RawMessage(fmt.Sprintf(`{"at":%d}`, i))); err != nil {
			t.Fatal(err)
		}
	}
	list := h.List()
	if len(list) != speedHistoryMax || string(list[0]) != fmt.Sprintf(`{"at":%d}`, speedHistoryMax+4) {
		t.Fatalf("cap / order: %d %s", len(list), list[0])
	}
	if _, err := h.Add(json.RawMessage(`[1,2]`)); err == nil {
		t.Fatal("non-object accepted")
	}
	if _, err := h.Add(json.RawMessage(`{"x":"` + string(make([]byte, speedHistoryRecordMax)) + `"}`)); err == nil {
		t.Fatal("oversized record accepted")
	}
	if err := h.Clear(); err != nil || len(h.List()) != 0 {
		t.Fatalf("clear: %v", err)
	}
}
