package core

import (
	"strings"
	"sync"
	"time"
)

// Line：一行日志
type Line struct {
	Seq    int64  `json:"seq"`
	Time   int64  `json:"time"`
	Source string `json:"source"` // core / agent / mesh
	Text   string `json:"text"`
}

// Ring：最近的日志行（只在内存，满了丢最旧的）
type Ring struct {
	mu    sync.Mutex
	lines []Line
	next  int64
	max   int
}

func NewRing(max int) *Ring { return &Ring{max: max} }

func (r *Ring) Add(source, text string) {
	text = strings.TrimRight(text, "\r\n")
	if text == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.next++
	r.lines = append(r.lines, Line{Seq: r.next, Time: time.Now().UnixMilli(), Source: source, Text: text})
	if len(r.lines) > r.max {
		r.lines = append(r.lines[:0], r.lines[len(r.lines)-r.max:]...)
	}
}

// Since：序号大于 after 的行（界面轮询增量）
func (r *Ring) Since(after int64, source string) []Line {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Line
	for _, l := range r.lines {
		if l.Seq > after && (source == "" || l.Source == source) {
			out = append(out, l)
		}
	}
	return out
}

func (r *Ring) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = nil
}
