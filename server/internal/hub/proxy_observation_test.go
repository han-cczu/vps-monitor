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

// 清理观测记录后换会话：老会话的地址变了，新会话随 config 下发给探针。
func TestRotateProxySessionIssuesNewSession(t *testing.T) {
	h := NewAgentHub(nil, nil, nil)
	h.SetConfigBuilder(func(int64) any {
		return proto.Config{Type: proto.TypeConfig, ReportInterval: 1}
	})
	ac := &agentConn{serverID: 1, send: make(chan []byte, 20), proxySession: "old", proxyManagement: "external"}
	h.conns[1] = ac
	if h.ProxySession(1) != "old" {
		t.Fatal("session not visible")
	}
	// 清理前采集的分页带的是老会话；换会话后它就不再被接受。
	if !h.RotateProxySession(1) {
		t.Fatal("rotation refused for an online connection")
	}
	rotated := h.ProxySession(1)
	if rotated == "" || rotated == "old" {
		t.Fatalf("session not rotated: %q", rotated)
	}
	var delivered proto.Config
	raw := <-ac.send
	if err := json.Unmarshal(raw, &delivered); err != nil || delivered.Type != proto.TypeConfig {
		t.Fatalf("rotation did not push a config: %s %v", raw, err)
	}
	if delivered.ProxyObserveSession != rotated {
		t.Fatalf("probe was told the wrong session: %q", delivered.ProxyObserveSession)
	}

	// 离线节点没有会话可换。
	delete(h.conns, 1)
	if h.RotateProxySession(1) {
		t.Fatal("rotation claimed success without a connection")
	}
	if h.ProxySession(1) != "" {
		t.Fatal("offline node still reports a session")
	}
}
