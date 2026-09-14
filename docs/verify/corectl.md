# Agent corectl 本地验收

日期：2026-09-14。基线：第 09、10 步已合入本地 main `036a794`；实现分支 `step/11-corectl`。

## 环境和范围

- Windows 上 Go 1.26.2 编译 Linux amd64/arm64 Agent，strip 后分别 13,369,506 / 12,583,074 字节。
- WSL Ubuntu 24.04 原生 systemd，真实 sing-box v1.14.0（步骤 10 的 amd64 构建）。
- 本次下载来自临时 loopback HTTP fixture，带 Bearer 验证与真实二进制 SHA256；面板下载鉴权本身在步骤 10 验过。本步没有接入公网节点。
- 自动测试拒绝覆盖已有核心文件、配置、状态和 unit。finally 停止本次 Agent/客户端/核心，删除本次 unit 和目录，daemon-reload。没有安装或开启本机防火墙。

## 已运行的检查

| 检查 | 结果 |
|---|---|
| 三模块 go vet / go test | 通过 |
| Agent、Server 独立模块 GOWORK=off go test | 通过 |
| Linux -race：corectl、transport、config、ping | 通过；覆盖状态、队列、文件锁、统计发送重试和握手顺序 |
| 前端 npm test / tsc / ESLint / build | 通过 |
| Linux amd64 / arm64 Agent 交叉编译 | 通过；只有 amd64 实际执行 |
| 真实核心下载、hash、version、unit | 通过 |
| 合法配置应用与端口监听、0600 权限 | 通过 |
| 非法配置 check 失败、旧服务保留 | 通过 |
| TCP 端口占用、失败回滚、旧 revision/hash 保留 | 通过 |
| SS2022 用户流量与 reset | 通过；1,000,000 字节 payload，对应 down=1,000,117、up=85；下一次全部为 0 |
| 真实日志 tail、stop/start/restart | 通过 |
| SIGKILL 后轮询变化 | 通过；测试临时禁用自动重启，约 9.52 s 发现 running=false |
| 真 ufw/firewalld 节点 | 未验证；规则及失败路径有 fake runner 单测 |

原始计数与逐项结果在 [corectl-smoke-amd64.json](corectl-smoke-amd64.json)。测试使用随机端口和随机临时密钥，证据不保存密钥、配置或 token。

第一次真实测试暴露 `start-limit-hit`：端口冲突导致核心退出后，连续启停达到 systemd 频率限制。已修复为显式 start/restart 前定向 reset-failed sing-box.service；真实回滚与启停复验通过。

## 复现

在可清理的 Linux systemd 测试环境，准备本仓库 Agent 和包含 with_v2ray_api 的核心。以下脚本要求 root；默认路径存在既有安装时会拒绝运行。

```sh
sudo python3 ci/corectl-smoke.py \
  --agent /absolute/path/vps-agent \
  --core /absolute/path/sing-box \
  --output /absolute/path/corectl-smoke.json
go test -race ./agent/internal/corectl ./agent/internal/transport ./agent/internal/config ./agent/internal/ping ./agent/cmd/agent
```

局限：systemd 默认 3 s 自动重启，10 s 轮询可能看不到短暂退出；不能把测试中禁用自动重启的结果视为捕获全部崩溃。core.stats 没有持久队列或应用层 ACK，reset 到送达之间崩溃可能丢失增量。第 13 步尚未实现服务端下发、状态处理与流量入库；第 14 步 UI 尚未实现。远端 CI/Release、公网部署、ARM64 执行和真实防火墙仍待验收。
