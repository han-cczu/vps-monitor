# VPS Monitor

[![CI](https://github.com/han-cczu/vps-monitor/actions/workflows/ci.yml/badge.svg)](https://github.com/han-cczu/vps-monitor/actions/workflows/ci.yml)

自托管的 VPS 监控与代理节点管理面板。Go 服务端内嵌 React 前端，节点通过 Agent 上报状态，配置和历史数据保存在 SQLite 中。

## 功能

- 节点实时监控、历史曲线、Ping 延迟、账单与流量套餐。
- sing-box 核心托管、配置预检与回滚，支持 VLESS Reality、Shadowsocks 2022、Hysteria2 和 TUIC。
- 订阅用户、多种订阅格式、流量配额、到期控制和中转管理。
- 告警规则、Webhook / Telegram 通知、TOTP、审计、备份和 Agent 更新。
- 中文管理界面，支持深浅色主题及移动端布局。

## 本地开发

需要 Go 1.26.8、Node.js 24 和 npm。以下命令从仓库根目录执行。

先安装前端依赖：

```sh
npm --prefix web ci
```

在两个终端分别启动：

```sh
# 终端一：服务端，默认监听 9000
go run ./server/cmd/server
```

```sh
# 终端二：前端，代理 API 和 WebSocket 到服务端
npm --prefix web run dev
```

访问 `http://localhost:8080`。首次启动会创建管理员 `admin`，随机密码仅在首次启动日志中输出；项目没有统一的默认密码。

默认数据目录为 `./data`，可通过 `VM_DATA_DIR` 指定。环境变量、反向代理、账号恢复与备份方式见 [运维手册](docs/runbook.md)。

## 检查与构建

```sh
go vet ./proto/... ./agent/... ./server/...
go test ./proto/... ./agent/... ./server/...
npm --prefix web test
npm --prefix web run lint
npm --prefix web run build
```

安装 GNU Make 后，可使用 `make build` 构建 Linux Agent 和内嵌前端的服务端。仅执行前端构建不会自动更新服务端；`make build-server` 会先同步前端构建产物再编译服务端。

部署配置位于 [deploy](deploy/)，恢复说明见 [restore.md](deploy/restore.md)。GitHub Actions 在推送 `main` 或创建 PR 时执行检查；`v*` 标签触发镜像与 Agent 发布，sing-box 构建工作流需手动运行。

## 项目结构

| 目录 | 内容 |
| --- | --- |
| `server/` | HTTP / WebSocket API、认证、SQLite、订阅、告警与任务调度 |
| `agent/` | 节点采集、连接、核心控制与自动更新 |
| `proto/` | 服务端与 Agent 共用的协议定义 |
| `web/` | React / MUI 管理界面 |
| `ci/` | 真实核心与集成验证脚本 |
| `docs/` | 设计、实施计划、进度、验收与运维文档 |

## 验证范围

20 步计划的代码已实现，本地工程检查与集成验证记录见 [实施进度](docs/进度.md)、[集成验收](docs/verify/completion.md) 和 [中文界面验收](docs/verify/chinese-ui.md)。公网部署、云防火墙、ARM64 实机、真实手机与通知渠道、多日运行等仍有待验项目；代码完成不代表这些环境已全部验收。

前端来源和使用约定见 [前端说明](web/README.md)，sing-box 的版本、构建标签及授权文件处理见 [核心版本说明](docs/core-version.md)。固定测试证书仅用于渲染测试，不能作为实际节点凭据。

数据库、密码、环境配置、依赖目录、构建产物和本机运行备份不随源码上传。
