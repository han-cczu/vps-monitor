package api

import (
	"context"
	"net/http"
	"testing"

	"vpsmon/server/internal/auth"
	"vpsmon/server/internal/hub"
	"vpsmon/server/internal/store"
)

// TestWebSocketRoutesBypassJWT 守住两个 WS 端点「不在 JWT 组内」这条接线约束。
//
// 这两条路由如果被挪进 protected 组，agent 和浏览器都会在握手阶段就拿到 401，
// 而 hub 包自己的测试直接打在 handler 上、根本看不见路由挂在哪——
// 审查用变异测试确认过：挪进去以后全部测试仍然是绿的。所以这条守在 api 包这边。
//
// 判据是「有没有走到 hub 的 handler」：走到了就会因为缺 Upgrade 头被 websocket.Accept
// 挡下（426 Upgrade Required），没走到就是中间件的 401。
func TestWebSocketRoutesBypassJWT(t *testing.T) {
	var db *store.DB
	env := newTestEnv(t, func(d *Deps) {
		db = d.DB
		d.Hub = hub.New(d.DB, d.Tokens)
	})

	token, err := auth.NewAgentToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateServer(context.Background(),
		store.ServerInput{Name: "ws-test", Currency: "CNY", BillingCycle: "month", TrafficResetDay: 1, TrafficMode: "sum"},
		auth.HashAgentToken(token)); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		path       string
		authHeader string
		wantStatus int
		why        string
	}{
		{
			name:       "浏览器端点不带 JWT 也能走到 hub",
			path:       "/api/ws",
			wantStatus: http.StatusUpgradeRequired,
			why:        "挂在 JWT 组里的话这里会是 401",
		},
		{
			name:       "agent 端点带合法 agent token 能走到 hub",
			path:       "/api/agent/ws",
			authHeader: "Bearer " + token,
			wantStatus: http.StatusUpgradeRequired,
			why:        "agent token 不是 JWT，挂在 JWT 组里会被中间件判 401",
		},
		{
			name:       "agent 端点的无效 token 由 hub 判 401",
			path:       "/api/agent/ws",
			authHeader: "Bearer not-a-real-token",
			wantStatus: http.StatusUnauthorized,
			why:        "hub 自己查 token_hash 后拒绝",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, env.srv.URL+tc.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tc.wantStatus {
				t.Errorf("期望 %d，得到 %d（%s）", tc.wantStatus, resp.StatusCode, tc.why)
			}
		})
	}
}

// TestWebSocketRoutesAbsentWithoutHub 说明没装 hub 时这两条路径不存在，
// 走 /api 的 JSON 404——api 包其余测试就是在这个装配下跑的。
func TestWebSocketRoutesAbsentWithoutHub(t *testing.T) {
	env := newTestEnv(t)

	for _, path := range []string{"/api/ws", "/api/agent/ws"} {
		resp, body := env.do(t, http.MethodGet, path, "", nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s：期望 404，得到 %d", path, resp.StatusCode)
		}
		if body["message"] != "not found" {
			t.Errorf("%s：期望 JSON 404，得到 %v", path, body)
		}
	}
}
