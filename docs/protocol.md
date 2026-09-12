# 协议与接口

> 本文件随代码走：每个步骤新增或改动的 WebSocket 消息、REST 接口都记到这里。
> 设计方案（`设计方案.md`）不改，改了就记到对应步骤文档的"偏离记录"。

## 1. WebSocket · agent ↔ server

端点：`wss://{面板域名}/api/agent/ws`

（步骤 04 起填充。消息结构体见 `proto/msg.go`。）

### 信封

```json
{ "type": "metrics", "id": "可选，请求响应配对用", "ts": 1757600000000, "data": {} }
```

| 方向 | type | 说明 | 引入步骤 |
|---|---|---|---|
| — | — | — | — |

## 2. WebSocket · 浏览器 ↔ server

端点：`wss://{面板域名}/api/ws`

（步骤 05 起填充。）

## 3. REST

| 方法 | 路径 | 鉴权 | 说明 | 引入步骤 |
|---|---|---|---|---|
| GET | `/api/health` | 无 | `{ "ok": true, "version": "dev" }` | 01 |

## 4. 数据表

（步骤 02 起填充。迁移文件在 `server/internal/store/migrations/`。）
