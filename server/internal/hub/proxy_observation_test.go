package hub

import (
	"encoding/json"
	"testing"
	"vpsmon/proto"
)

func TestFinalSendGuardAndNegotiatedConfig(t *testing.T) {
	h := NewAgentHub(nil, nil, nil)
	ac := &agentConn{serverID: 1, send: make(chan []byte, 20)}
	h.conns[1] = ac
	for _, mode := range []string{"unknown", "external", "none", "managed"} {
		ac.proxyManagement = mode
		ac.proxySession = "session"
		if mode == "unknown" {
			ac.proxySession = ""
		}
		for _, message := range []any{proto.CoreApply{Type: proto.TypeCoreApply}, proto.CoreAction{Type: proto.TypeCoreAction, Action: "restart"}, proto.CoreLogsReq{Type: proto.TypeCoreLogs}} {
			if h.SendTo(1, message) != (mode == "managed") {
				t.Fatal("unsafe managed dispatch", mode)
			}
		}
		if h.SendTo(1, proto.CoreAction{Type: proto.TypeCoreAction, Action: "install"}) != (mode == "managed" || mode == "none") {
			t.Fatal("unsafe install dispatch", mode)
		}
		if !h.SendTo(1, proto.Config{Type: proto.TypeConfig}) {
			t.Fatal("config blocked")
		}
		for len(ac.send) > 0 {
			raw := <-ac.send
			var c proto.Config
			json.Unmarshal(raw, &c)
			if c.Type == proto.TypeConfig && c.ProxyObserveSession != ac.proxySession {
				t.Fatal("session lost on config refresh")
			}
		}
	}
}
