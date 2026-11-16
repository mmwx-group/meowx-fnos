package store

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// 下载时按应答头存订阅键 / 内容指纹 / 实时通道地址 / 只读凭证，没有就清空；推送触发的重拉带 mmwx-refresh: push
func TestProfileRealtimeHeaders(t *testing.T) {
	var withHeaders atomic.Bool
	var lastRefresh atomic.Value
	withHeaders.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastRefresh.Store(r.Header.Get("mmwx-refresh"))
		if withHeaders.Load() {
			w.Header().Set("mmwx-sub-key", "pkg:12")
			w.Header().Set("mmwx-sub-rev", "9b1e0c3a77d2")
			w.Header().Set("mmwx-realtime-url", "https://panel.example.com")
			w.Header().Set("mmwx-realtime", "rt-secret")
		}
		_, _ = w.Write([]byte("proxies:\n  - {name: a, type: ss, server: 1.2.3.4, port: 1, cipher: aes-128-gcm, password: x}\n"))
	}))
	defer srv.Close()

	s, err := OpenProfiles(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.Client = srv.Client()
	if err := s.upsert(Profile{ID: "p1", URL: srv.URL + "/sub"}); err != nil {
		t.Fatal(err)
	}

	p, err := s.Update(WithPushRefresh(context.Background()), "p1")
	if err != nil {
		t.Fatal(err)
	}
	if lastRefresh.Load() != "push" {
		t.Fatalf("push refresh header: %q", lastRefresh.Load())
	}
	if p.SubKey != "pkg:12" || p.SubRev != "9b1e0c3a77d2" || p.RealtimeURL != "https://panel.example.com" || p.Realtime != "rt-secret" {
		t.Fatalf("headers not stored: %+v", p)
	}
	if got, _ := s.Get("p1"); got.SubRev != "9b1e0c3a77d2" {
		t.Fatalf("not persisted: %+v", got)
	}

	withHeaders.Store(false)
	p, err = s.Update(context.Background(), "p1")
	if err != nil {
		t.Fatal(err)
	}
	if lastRefresh.Load() != "" {
		t.Fatalf("normal refresh sent mmwx-refresh: %q", lastRefresh.Load())
	}
	if p.SubKey != "" || p.SubRev != "" || p.RealtimeURL != "" || p.Realtime != "" {
		t.Fatalf("headers not cleared: %+v", p)
	}
}
