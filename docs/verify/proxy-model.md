# 代理数据模型与密钥证书验收

日期：2026-09-14。分支：step/12-proxy-model，基线 9831769。使用独立测试 SQLite，不迁移当前运行面板的数据。

| 验收内容 | 证据 |
|---|---|
| 9 张表、既有节点回填、新节点 core 初始化 | store/proxy_test.go：从 0004 升至 0005，重复迁移、回退、重放、foreign_key_check |
| 外键级联、历史流量保留 | proxy/service_test.go：删节点删入站/证书/修订/分配，保留已计入用户额度的节点流量；删用户级联删流量 |
| TCP/UDP 冲突、并发保护 | 四协议组合表驱动；两个 Service 并发创建相同端口只成功一个；原生 SQL INSERT/UPDATE 同样受 trigger 保护 |
| UUID/X25519/short_id/PSK/password/token | keys_test.go：128 轮格式/长度/不重复断言、公钥独立推导、私钥 clamp |
| 自签证书 | certs_test.go：P-256、公私钥匹配、序列号、SAN、有效期、用途、TLS 验证、SHA256；Windows/Linux OpenSSL 独立解析并核对指纹 |
| HTTP 鉴权和接口 | api/proxy_test.go：20 条路由的 JWT 保护，未登录和 Agent Token 都返回 401；实际 HTTP + SQLite CRUD |
| 凭据与列表/证书脱敏 | API 列表不带四项凭据、GET cert 无 key_pem、所有代理响应 no-store；审计递归检查敏感字段均为 *** |
| 分配和通知 | 全量替换，错误 ID 不部分删除，通知新旧节点去重；回调确认数据已提交 |
| 重置语义 | 凭据与 token 独立旋转；清空当前账期汇总及 traffic_used，保留历史，不恢复手动禁用或 expired |
| 事务失败 | 审计插入失败时入站/自动证书一起回滚；取消事务后连接可复用，无残留事务 |

已执行并通过：

```sh
go vet ./proto/... ./agent/... ./server/...
go test ./proto/... ./agent/... ./server/...
# server/：独立模块，关闭工作区
GOWORK=off go test ./internal/proxy/... ./internal/store ./internal/api
# WSL Ubuntu 24.04 / Go 1.26.2 / GCC
go test -race ./server/internal/proxy/... ./server/internal/store ./server/internal/api
# web/
npm test
npm run tsc:check
npm run lint
npm run build
```

Server 在 Windows 构建并执行 version，Linux amd64/arm64 的 CGO_ENABLED=0 构建通过；未在 ARM64 真机运行。没有新外部 Go 依赖。

本步未实现第 13 步渲染/下发/统计入库、第 14/15 步 UI 与订阅输出、第 16 步限额/到期执行。凭据重置目前修改数据库并通知 NoopNotifier；不会改变运行中节点配置。远端 CI 未运行（仓库无 remote），已有公网与真实防火墙验收缺口维持原记录。
