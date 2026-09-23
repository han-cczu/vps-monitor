# VPS Monitor

[![CI](https://github.com/han-cczu/vps-monitor/actions/workflows/ci.yml/badge.svg)](https://github.com/han-cczu/vps-monitor/actions/workflows/ci.yml)

VPS Monitor 是自托管的 VPS 监控与代理节点管理面板。节点上的 Agent 通过 WebSocket 向 Go 服务端上报数据；服务端提供 API、管理界面和订阅，并用 SQLite 保存配置与历史记录。生产部署由 Caddy 提供 HTTPS，前端构建后内嵌在服务端中。

## 能做什么

| 场景 | 功能 |
| --- | --- |
| 节点监控 | 实时状态、历史曲线、Ping 探测、账单与流量套餐；可分别校准入站和出站流量。 |
| 托管代理 | 管理 sing-box 核心与配置，预检失败时保留旧配置；支持 VLESS Reality、Shadowsocks 2022、Hysteria2、TUIC。 |
| 外部代理观测 | 查看已有 sing-box / Xray 实例的运行与入站信息。外部管理实例在面板中只读，不接管其配置、重启或卸载。 |
| 订阅与用量 | 订阅用户、多种订阅格式、流量配额、到期控制与中转管理。 |
| 运维 | 告警规则、Webhook / Telegram 通知、TOTP、审计、备份，以及面板和 Agent 版本检查与更新入口。 |

管理界面提供中文、深浅色主题和移动端布局。托管代理需要在目标 Linux 节点安装 Agent；第三方代理观测的支持范围与限制见[验收记录](docs/verify/external-proxy-observation.md)。

## 本地运行

需要 Go 1.26.8、Node.js 24 和 npm。仓库根目录是包含 `go.work` 的 Go 工作区，下面的命令均从仓库根目录执行。

先安装前端依赖：

```sh
npm --prefix web ci
```

打开两个终端，分别启动服务端和前端：

```sh
# 终端一：Go 服务端，默认监听 :9000
go run ./server/cmd/server
```

```sh
# 终端二：Vite 前端，默认监听 :8080，并将 /api 代理到 :9000
npm --prefix web run dev
```

访问 <http://localhost:8080>；服务端健康检查为 <http://localhost:9000/api/health>。首次启动会创建管理员 `admin`，随机初始密码只在首次启动日志中输出一次。登录后请到「设置 → 账号」修改密码；项目没有通用默认密码。

本地数据默认写入 `./data`，也可通过 `VM_DATA_DIR` 指定目录。新建节点后，面板会一次性显示 Agent token 和安装命令。若要在本地使用该命令，须先构建 Agent，并将产物放入数据目录；步骤见[运维手册](docs/runbook.md#节点与-agent-分发步骤-03)。

## Docker 部署

仓库的 [Compose 配置](deploy/docker-compose.yml)启动 Go 服务端和 Caddy。Caddy 对外使用 80/443，服务端的 9000 端口只在 Compose 内部网络使用。部署前需要准备指向目标主机的域名、Docker 与 Compose，并放通 80/443。

`deploy/.env.example` 中的 `SERVER_IMAGE` 是占位地址。若尚未发布镜像，可在**部署主机**的仓库根目录构建镜像，再复制部署文件：

```sh
sudo docker build -f deploy/Dockerfile -t vps-monitor-server:local .
sudo mkdir -p /opt/vps-monitor
sudo cp deploy/docker-compose.yml deploy/Caddyfile deploy/.env.example /opt/vps-monitor/
cd /opt/vps-monitor
sudo cp .env.example .env
sudo chmod 600 .env
```

编辑 `/opt/vps-monitor/.env`，至少填写 `PANEL_DOMAIN`、`VM_JWT_SECRET` 和 `SERVER_IMAGE=vps-monitor-server:local`。`VM_JWT_SECRET` 至少 32 字节，可用 `openssl rand -hex 32` 生成。随后启动并查看首次登录密码：

```sh
sudo docker compose up -d
sudo docker compose logs --tail=100 server
```

浏览器访问 `https://<PANEL_DOMAIN>`。业务数据保存在部署目录下的 `./data`；升级前请先备份。镜像发布、反向代理、备份恢复、密码重置和排障步骤见[运维手册](docs/runbook.md)与[恢复说明](deploy/restore.md)。

## 检查与构建

```sh
go vet ./proto/... ./agent/... ./server/...
go test ./proto/... ./agent/... ./server/...
npm --prefix web test
npm --prefix web run tsc:check
npm --prefix web run lint
npm --prefix web run build
```

安装 GNU Make 后，`make build` 可构建 Linux amd64/arm64 Agent 和内嵌前端的服务端；`make build-server` 会先同步前端构建产物。仓库根目录没有 `go.mod`，运行 Go 检查时需显式列出三个模块。CI 在推送 `main` 或创建 PR 时运行检查；`v*` 标签触发镜像和 Agent 发布，sing-box 构建工作流需手动触发。

## 项目结构与文档

| 路径 | 内容 |
| --- | --- |
| `server/` | HTTP / WebSocket API、认证、SQLite、订阅、告警与任务调度。 |
| `agent/` | 节点采集、连接、代理核心控制与更新。 |
| `proto/` | 服务端与 Agent 共用的协议定义。 |
| `web/` | React 管理界面。 |
| `deploy/` | Docker、Caddy、备份与恢复文件。 |
| `ci/` | 集成验证脚本。 |
| `docs/` | 设计、实施计划、验收记录与运维手册。 |

更多信息：[前端说明](web/README.md) · [核心版本说明](docs/core-version.md) · [实施进度](docs/进度.md) · [集成验收](docs/verify/completion.md)。验收文档记录的是对应日期与环境的结果，实际部署仍需按目标环境验证。

数据库、密码、环境配置、依赖目录和构建产物不随源码上传。固定测试证书只用于渲染测试，不能作为实际节点凭据。
