package store

import (
	"encoding/json"
	"errors"
	"os"
	"sync"
)

// SpeedHistory：网页「网络测速」测完 / 失败的记录（/data/speedtest-history.json），新的在前，最多 speedHistoryMax 条。
// 每条的内容由网页定（时间、节点、测速源、速率…），这里原样存、只限大小。
type SpeedHistory struct {
	mu   sync.Mutex
	path string
}

const (
	speedHistoryMax       = 50
	speedHistoryRecordMax = 4 << 10
)

func OpenSpeedHistory(path string) *SpeedHistory { return &SpeedHistory{path: path} }

func (h *SpeedHistory) loadLocked() []json.RawMessage {
	var list []json.RawMessage
	if data, err := os.ReadFile(h.path); err == nil {
		_ = json.Unmarshal(data, &list)
	}
	return list
}

// List 全部记录（新的在前）
func (h *SpeedHistory) List() []json.RawMessage {
	h.mu.Lock()
	defer h.mu.Unlock()
	list := h.loadLocked()
	if list == nil {
		list = []json.RawMessage{}
	}
	return list
}

// Add 记一条（必须是 JSON 对象），返回记过之后的全部记录
func (h *SpeedHistory) Add(record json.RawMessage) ([]json.RawMessage, error) {
	if len(record) > speedHistoryRecordMax {
		return nil, errors.New("记录太大")
	}
	var obj map[string]any
	if json.Unmarshal(record, &obj) != nil {
		return nil, errors.New("记录不是 JSON 对象")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	list := append([]json.RawMessage{record}, h.loadLocked()...)
	if len(list) > speedHistoryMax {
		list = list[:speedHistoryMax]
	}
	data, err := json.Marshal(list)
	if err != nil {
		return nil, err
	}
	return list, WriteFileAtomic(h.path, data, 0o600)
}

// Clear 清空
func (h *SpeedHistory) Clear() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := os.Remove(h.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
