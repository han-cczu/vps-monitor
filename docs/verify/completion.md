# 步骤 15–20 集成验收

日期：2026-09-14。代码实现、真实本地链路、浏览器 fixture 与外部验收分别记录。仓库未配置 remote，本记录不代表远端 CI、Release 或生产发布。

后续交付说明：本文件记录首次本地集成时的状态。之后已补齐中文公共界面，并创建公开仓库 [han-cczu/vps-monitor](https://github.com/han-cczu/vps-monitor)；当前远端检查请查看对应 main 提交的 [Actions](https://github.com/han-cczu/vps-monitor/actions/workflows/ci.yml)。本地清理已将临时产物和冗余依赖移出项目，保留运行程序、数据库、备份与必要工具。

## 工程检查

- Windows Go 1.26.8：`go test -p 2 ./server/... ./agent/... ./proto/...`、`go vet -p 2 ./server/... ./agent/... ./proto/...` 全部通过。
- WSL Ubuntu-24.04 / Linux amd64 / Go 1.26.8：`CGO_ENABLED=1 GOMAXPROCS=2 go test -race -p 2 ./server/... ./agent/... ./proto/...` 全部通过。包含告警投递、流量结算、限额、认证、维护和 Agent 更新。
- Linux amd64/arm64 Server 与 Agent 交叉构建通过；arm64 仅构建，未运行。
- Web：13 项 Node 测试、全量 ESLint、TypeScript、生产构建通过；手机告警卡片修复后再次通过 TypeScript、定向 ESLint 和生产构建。Vite 有既有大型图表 chunk 提示，构建成功。
- Go 扫描无可达/已导入包漏洞；npm 生产及全量扫描均为 0。未使用的 openpgp 模块公告仍有提示，详见 [security-ops.md](security-ops.md)。

## 真实 Linux 链路

自动化入口：`ci/reconcile-smoke.py`，参数 `--mihomo --agent-next --relay`。Server/Agent 使用 Go 1.26.8 构建，核心为已校验的 sing-box v1.14.0，客户端 Mihomo v1.19.30。最终 27 项检查全部通过；完整无凭据证据见 [completion-smoke-amd64.json](completion-smoke-amd64.json)。

- Server → Agent → systemd → sing-box 实际安装、四协议五监听项、快速修改合并、应用修订、50 MiB 用户计费、占用端口保持旧配置及回滚、日志、断线重连、空 SS 用户拒绝旧凭据、面板重启保持修订均通过。
- Mihomo 导入真实订阅，VLESS/SS2022/HY2/TUIC 逐条实际传输；HY2/TUIC 不跳过验证，错误证书指纹均拒绝连接；完整 Clash 配置校验通过。
- sing-box 格式原样取四种出站，分别嵌入独立客户端配置并 check/run，每种协议通过真实代理获得完整 64 KiB 内容，TLS 保留订阅生成的校验设置。
- 给用户 100 MiB 额度，实际下载 150 MiB 后 19.547 秒内停用；新连接拒绝、订阅为空。提高至 1 GiB 后 6.251 秒内恢复；过期/延长到期日的拒绝与恢复、轮换订阅 token 后旧 URL 404 且代理凭据仍有效均通过。
- 本地 A→B→64 KiB 负载通过；停止 B 后同一路径失败，重启 B 恢复；删除中转后 B 停止时 A direct 仍成功。B 配置来自 relay API 所生成的专属身份与修订，B 为额外本地核心，无 Agent/统计；不代表双 VPS 出口 IP 或 B 分账验收，见 [relay-smoke.md](relay-smoke.md)。
- Agent v0.1.0→v0.1.1 的下载摘要、旧 `.bak` 摘要、同 PID exec、重新连接与上报均通过，耗时 3.311 秒。两版本为相同源码的不同版本号测试构建，不代表正式发布。
- Caddy v2.11.4 真实容器：正常订阅和故意上游 502 的 access/error 日志均无测试 token/Referer，`validate` 通过。入口 `ci/caddy-smoke.py`。测试结束清理自建容器及核心服务。

## 浏览器验收

使用 `ci/ui-fixture.py` 建立独立数据库和固定临时账号；原面板账号、节点和通知渠道不参与这些操作。前端生产构建通过本地 preview 访问后端。总览/账单无采集的节点为离线 fixture，用户非零流量为布局数据，不作为实际计费证明。

- 登录、总览实时通道、两节点卡片与套餐余额、超额/到期停用 2 人汇总正常。
- 桌面和 400×844：四种订阅用户状态、账期与额度、列表操作按钮、编辑基础/额度/分配、保存后详情备注与上下行分账/30 天图表正常。
- 四种订阅 URL 与二维码可切换，手机弹窗全屏且操作可见；URI 对 TUIC 不校验证书、HY2 依赖客户端 pin 支持的说明明确显示。
- 订阅模板错误 YAML 保存被拒并显示“订阅模板 YAML 不合法”，恢复默认后保存成功。
- 站点标题、时区 UTC、单位 1024 实际保存成功；顶栏标题变化，用户用量由 314.57 MB / 1.07 GB 改为 300 MiB / 1 GiB，日期按新时区呈现。
- 审计按 admin / settings.update / 起始时间筛选只剩此次变更，详情列出修改前后标题、时区和单位。
- 隔离账号通过 API 启用 TOTP 后，浏览器密码步骤进入六位验证码页面；有效验证码完成登录并返回原告警页面。重放/并发凭据变更/故障锁定边界由确定性后端测试覆盖。
- 告警事件、规则、空渠道列表及新增渠道弹窗正常。发现手机规则表参数被裁切后，改为堆叠卡片；400 像素下页面宽度无溢出，CPU 百分比编辑保存、刷新后仍为 92。未向第三方发送测试通知。
- 节点详情账单周期、价格、当前流量、历史账期表正常；完整真实手机扫码仍待外部设备验收。

开发服务在生产构建期间退出曾导致动态模块加载失败；切换独立生产 preview 并重载后继续验收。此条不作为业务异常忽略，也不将短暂加载画面当作稳定布局。

## 数据与并发收尾

- 账期在首批跨日采样前滚动，避免把新日流量丢到旧 epoch；日期锚防止切时区及重启重复清零。
- 保持 0007 原迁移不变，新增 0012 `period_date`；实际 0007/0011 旧库升级、重复迁移与 Initialize、全新库均通过。
- `ci/upgrade-smoke.py` 对原 Step08 在线数据库只读备份后启动新版：迁移 3→12，原 1 个用户、2 个节点、1684 条分钟和 30 条小时数据保留，原密码登录、新设置与订阅 API、完整性检查均通过。源库未修改；副本和临时进程自动清理。见 [upgrade-smoke.json](upgrade-smoke.json)。
- MFA 绑定、ticket 和最终提交均校验凭据版本，DB 故障不累计锁定；设置/TOTP 审计与写入同事务。
- 告警候选在发送前重新持久领取，取消/停用已提交的候选不再发送；16 并发只领取一次，重启租约及最多 3 次限制通过。HTTP 在事务外。外部成功但本地确认前崩溃仍可能重发，详见 [alerts-delivery-guard.md](alerts-delivery-guard.md)。

## 本地运行交付

后续界面补齐：用户指出外观抽屉仍有模板英文，已补齐公共组件中文并完成浏览器验收。当前本机版本已更新为 `local-steps20-zh`，程序为 `dist/local-panel/vps-server-zh.exe`，详见 [公共界面中文验收](chinese-ui.md)。以下为步骤 15–20 首次交付时的运行快照。

集成提交 `8023bdc` 已快进合入 main。原 Step08 Windows 后端在一致性备份后替换为最终构建（版本标识 `local-steps20`），地址 `http://localhost:9000/dashboard`；前端开发端口8080保持运行。原数据库原地迁移至12，原账号密码与两节点保留，现有Agent重新在线。健康、认证、新设置/订阅接口和SQLite完整性检查均通过。

运行程序为 `dist/local-panel/vps-server.exe`，本轮PID 14736；运行信息/检查结果为同目录 `runtime.json`、`verification.json`，日志为 `server.log` / `server-error.log`。原数据路径沿用旧进程配置；一致性备份为 `dist/local-panel/backup-before-steps20-20260914-212016/vm.db`。这些是本机运行状态，后续可能变化；未配置开机启动或远端部署。

回退旧程序必须同时恢复该备份数据库：停止新版后，用备份恢复数据库并处理对应WAL/SHM，再按原环境配置启动旧可执行文件。不要让旧版本直接写入已升级的数据库。恢复会回到备份时点，后续新写入需另行保留。

临时8085/9015验收进程已停止，WSL测试核心服务inactive且自建核心路径已清理，Caddy测试容器已移除。旧版fixture脚本的SQLite连接未关闭导致退出清理失败，现已修复；遗留 `dist/step15-20-qa/ui-pf8_jl16` 与 `upgrade-wgdt31ph` 的删除被自动审批策略拒绝，保留在忽略目录中，约0.8MiB。没有绕过策略继续删除。

## 尚需目标环境

公网域名/TLS 与干净 VPS 安装、云防火墙、ARM64 实机、真实手机/URI 客户端扫码、两台 VPS 出口 IP 与 B 分账、真实 Telegram、实际资源压力及公网 Ping、连续一天 vnstat 对照、多日日志轮转/WAL、正式镜像/Release 更新及远端 CI 均未由本地测试替代。需要相应设备、目标环境或仓库 remote 后完成。
