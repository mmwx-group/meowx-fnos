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

func TestSpeedHistorySetBoard(t *testing.T) {
	h := OpenSpeedHistory(filepath.Join(t.TempDir(), "h.json"))
	for _, rec := range []string{`{"id":"a","at":1}`, `{"at":2}`, `{"id":"b","at":3}`} {
		if _, err := h.Add(json.RawMessage(rec)); err != nil {
			t.Fatal(err)
		}
	}
	list, err := h.SetBoard("a", json.RawMessage(`{"status":"uploaded","id":"X1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(list[2]); got != `{"at":1,"board":{"status":"uploaded","id":"X1"},"id":"a"}` {
		t.Fatalf("set: %s", got)
	}
	if string(list[0]) != `{"id":"b","at":3}` || string(list[1]) != `{"at":2}` {
		t.Fatalf("others changed: %s %s", list[0], list[1])
	}
	if list, err = h.SetBoard("a", json.RawMessage(`null`)); err != nil || string(list[2]) != `{"at":1,"id":"a"}` {
		t.Fatalf("clear: %v %s", err, list[2])
	}
	if list, err = h.SetBoard("missing", json.RawMessage(`{"status":"failed"}`)); err != nil || len(list) != 3 {
		t.Fatalf("missing id: %v %d", err, len(list))
	}
	if _, err = h.SetBoard("a", json.RawMessage(`[1]`)); err == nil {
		t.Fatal("non-object board accepted")
	}
	if _, err = h.SetBoard("", json.RawMessage(`{}`)); err == nil {
		t.Fatal("empty id accepted")
	}
	if got := h.List(); string(got[2]) != `{"at":1,"id":"a"}` {
		t.Fatalf("persisted: %s", got[2])
	}
}

func TestSpeedBoard(t *testing.T) {
	path := filepath.Join(t.TempDir(), "speedboard.json")
	b := OpenSpeedBoard(path)
	st, err := b.Get()
	if err != nil || st.Upload != nil || !installIDPattern.MatchString(st.InstallID) {
		t.Fatalf("get: %+v %v", st, err)
	}
	again, _ := OpenSpeedBoard(path).Get()
	if again.InstallID != st.InstallID {
		t.Fatalf("install_id not kept: %s vs %s", again.InstallID, st.InstallID)
	}
	if st, err = b.SetUpload(true); err != nil || st.Upload == nil || !*st.Upload || st.InstallID != again.InstallID {
		t.Fatalf("set: %+v %v", st, err)
	}
	reset, err := b.Reset()
	if err != nil || reset.InstallID == st.InstallID || !installIDPattern.MatchString(reset.InstallID) || reset.Upload == nil || !*reset.Upload {
		t.Fatalf("reset: %+v %v", reset, err)
	}
}
