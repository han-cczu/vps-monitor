# 渲染器与下发闭环验收

日期：2026-09-14。分支：step/13-reconciler，基线 6a47aed（11/12 已合入本地 main）。

## 自动化验证

| 验证 | 内容 |
|---|---|
| 渲染黄金文件 | 四协议各一、全协议、空用户、无入站、高级 JSON 共 8 组；稳定顺序、紧凑 JSON 哈希、端口/用户清单、禁用过滤 |
| 真实核心 | Linux amd64 sing-box v1.14.0 对全部样例执行 check；未知字段被拒绝；密钥 stderr 不返回 API |
| 对齐器 | 5 s debounce 的短时钟等价测试；预检期间写入使旧快照失效；失败预检/审计不留下半个修订 |
| 消息一致性 | 离线保存、重连等待新状态、req_id 隔离、旧 ACK 不清新请求、旧修订 ACK 后发送最新、超时/退避/耗尽、重发复用修订 |
| 版本与回滚 | 不自动升级；旧内容作为新修订；保留最近 20 条；周期复核保留回滚；面板恢复旧库后修订号高于 Agent |
| 流量 | 50 MiB 模拟消息、实际传输（见下）；按面板接收日期；跨节点过滤、负数/超大/重复计数校验、并发入桶/落库 |
| 流量重置与恢复 | 清零 epoch 隔离旧缓存；数据库失败整体回滚并合并新批次；删用户/换账期不恢复旧用量；删节点保留已接收用量；退出冲刷 |
| REST + WebSocket | 7 条新增路由的 JWT/Agent Token 隔离；真实 httptest WebSocket 串联安装、四协议 apply、状态、快照、统计、日志、回滚和节点删除 |

```sh
# 仓库根目录
go vet ./proto/... ./agent/... ./server/...
go test ./proto/... ./agent/... ./server/...
# Linux Go 1.26.2 / GCC；真实核心测试须设置该变量
SING_BOX_BIN=/absolute/path/sing-box-v1.14.0-linux-amd64 \
  go test -race ./server/internal/proxy/... ./server/internal/store \
  ./server/internal/api ./server/internal/hub ./server/internal/corefiles
# web/
npm test
npm run tsc:check
npm run lint
npm run build
```

未设置 SING_BOX_BIN 时，TestRealCoreChecks 明确 skip，不能据普通 go test 宣称真实核心已验。本轮在 WSL Ubuntu 24.04 设置了该变量并实际执行；sing-box 两架构构建工作流也已加入该检查，仓库无 remote，远端未执行。

Server 的 Windows amd64 构建并运行 version、Linux amd64/arm64 的 CGO_ENABLED=0 构建均通过；Linux amd64 产物用于下方实际验收。前端保留既有大分块提示，检查和构建成功。

## 真实生命周期

`ci/reconcile-smoke.py` 使用真实 Linux Server、Agent、sing-box 与 systemd，面板存储放在测试专用临时目录，管理员密码/Agent Token/代理凭据随机生成。通过正式 REST API 上传双架构资产、选择当前核心和创建入站/用户；通过正式 Agent WebSocket 下发和回流，不使用伪造 core.state 替代 Agent。

```sh
# 在没有既有 sing-box 的 Linux/systemd 测试环境，以 root 运行
python3 ci/reconcile-smoke.py \
  --server /absolute/path/server \
  --agent /absolute/path/vps-agent \
  --core /absolute/path/sing-box-v1.14.0-linux-amd64 \
  --core-arm64 /absolute/path/sing-box-v1.14.0-linux-arm64 \
  --output /absolute/path/smoke.json
```

最终实测：50 MiB 载荷（52,428,800 字节），用户累计 52,429,003 字节，43.402 s 内由定时任务落库。机器可读结果保存在 [reconciler-smoke-amd64.json](reconciler-smoke-amd64.json)，复验时以对应记录为准。

验收涵盖安装/版本校验、四协议五监听项、修订 1、三次修改合并、SS2022 真实传输、外部进程占用端口后的旧配置恢复、显式回滚哈希、真实日志、离线编辑重连、空用户拒绝旧凭据及面板重启恢复。核心监听在测试 WSL 内，传输目标为回环 HTTP 服务，无公网代理节点。

脚本开头拒绝已有 `/usr/local/bin/sing-box`、`/etc/sing-box`、`/var/log/sing-box`、`/var/lib/vps-agent` 和 sing-box unit；finally 停止本次进程/服务并清理这些本次新建路径及临时目录。未重启原有 Windows 面板与 Agent。真实防火墙结果为 none，未验证 ufw/firewalld 的规则操作；ARM64 只交叉编译，未在 ARM64 真机运行；未做公网装机/TLS 验收。

## 统计与执行边界

API 保存成功与 Agent 应用成功分开：安装/重启只返回 queued 和 req_id；以 core 状态及 desired/applied/hash 判断结果。选择新当前版本不会自动升级 Agent。Windows 面板不能运行 Linux 预检核心，记录 WARN 后交由 Agent check；本轮完整闭环在 Linux 上运行。

统计是每 10 s Agent 读取 reset 增量、每 60 s 面板落库；失败批次在内存重试，正常退出冲刷已接受样本。没有统计 ACK 或持久消息队列，强杀或 reset 后断链可能丢失/重复，不保证精确一次。限额和到期自动执行、订阅输出以及页面分别留给后续步骤。
