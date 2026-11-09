package mesh

import "testing"

// 没往来的对端：Active=false，网页显示「空闲」；Relay 照样带着（只是对端的 home DERP）
func TestPeerViewActive(t *testing.T) {
	idle := peerView(&tsPeer{HostName: "istoreos", Online: true, Relay: "meowx-6", TailscaleIPs: []string{"100.108.193.93"}})
	if idle.Active || idle.Direct || idle.Relay != "meowx-6" || idle.IP != "100.108.193.93" {
		t.Fatalf("idle peer: %+v", idle)
	}
	busy := peerView(&tsPeer{HostName: "nas", Online: true, Relay: "meowx-6", Active: true})
	if !busy.Active || busy.Direct {
		t.Fatalf("relayed peer: %+v", busy)
	}
	direct := peerView(&tsPeer{HostName: "mac", Online: true, Relay: "meowx-6", CurAddr: "203.0.113.5:41641", Active: true})
	if !direct.Direct {
		t.Fatalf("direct peer: %+v", direct)
	}
}
