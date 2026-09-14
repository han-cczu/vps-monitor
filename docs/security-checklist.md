# 安全检查表

核对日期：2026-09-14。下面的“通过”只覆盖写明的环境和证据，不表示已经完成公网部署、真实手机或 ARM64 验收。步骤 20 的命令与故障用例见 [验收记录](verify/security-ops.md)，恢复步骤见 [runbook](runbook.md)。

| 项目 | 已观察结果 | 边界 / 发布前复核 |
|---|---|---|
| Agent 命令白名单 | 自更新只接受稳定更高版本、固定本机架构文件名、合法 SHA-256；畸形/多余字段/尾随 JSON/超长消息/降级/重定向均有拒绝测试 | 不添加任意 shell、任意下载 URL 或任意路径入口；旧 Agent 未声明能力时跳过 |
| 面板私有 | 已注册受保护 API 对匿名和 Agent token 返回 401；安全、设置、审计接口使用管理员 JWT；敏感响应 no-store | 未匹配 API 为 404、错误方法为 405，不泄露数据；浏览器登录跳转由集成验收覆盖 |
| Token 分离 | 管理员 JWT 与节点 token 验证路径分离；既有节点 token 重置测试检查旧 token 失效 | 节点 token 只允许该节点通道/资产下载，不作为管理员凭据；订阅 token 与连接凭据分别旋转 |
| TOTP | RFC 向量、密文篡改/错用户/错主密钥、一次性过期 ticket、IP/账户防暴力、持久防重放、改密后的旧绑定拒绝、并发凭据 CAS 均通过 | 限速/ticket 是单进程内存状态；重启会清空，但已用码保留在 SQLite；既有 JWT 生命周期未改为逐请求密码版本校验 |
| TOTP 原子性 | 开关、使用码消耗和审计同事务；审计故障全部回滚；旧 password_hash/secret/enabled 快照不可完成验证 | `reset-totp` 是拥有服务器运维权限的恢复入口，不提供公开恢复 API |
| sing-box 统计接口 | 渲染固定 127.0.0.1:10085，并拒绝与入站 TCP 端口冲突；前序 Linux/systemd 核心验收见 [记录](verify/reconciler.md) | 每台生产节点仍应执行 `ss -lntp` 检查回环绑定，并核查云安全组 |
| TLS 证书 pin | 集成已实际使用 Mihomo 连接四协议；订阅输出的 pin 保留在配置中 | 错误 pin 的真实负向连接测试由集成补齐；公网证书链与 HTTPS 重定向未在步骤 20 独立验证 |
| 审计完整性与脱敏 | `TestProxyAPICRUDSecretsAndAuth` 检查 14 类写操作、操作者/IP、JSON 有效性及递归秘密字段脱敏；设置/TOTP 另有原子性用例 | 审计保留期可缩短；数据库备份本身仍包含代理凭据，应限制访问 |
| Caddy 日志 | 主线提交 b93c754 的 `ci/caddy-smoke.py` 在真实 Docker Caddy v2.11.4 验证：订阅路径、query token、Referer、故意上游 502 均不在 access.log 或默认 error logger 泄露测试 token；配置 validate 通过 | access 过滤和默认/error logger 过滤必须同时保留；此测试是本地 HTTP fixture，不是公网 TLS 验证 |
| 供应链与更新 | 初始 Agent 安装和自动更新均校验 SHA-256；自动更新另校验 `--version`、大小/时间/架构/路径；sing-box 沿用托管摘要校验 | SHA-256 验证传输产物一致性，不是独立发布签名；信任已配置面板和发布过程 |
| 更新替换与恢复 | 自更新失败保留旧文件的定向测试通过；主线在真实 Linux amd64/systemd 上观察目标版本、旧 `.bak` 哈希、相同 PID、3.339 秒重连 | 本地同代码 v0.1.0→v0.1.1 测试构建，非真实远端 Release；进入新进程后持续崩溃按 runbook 手动恢复；ARM64 真机待验 |
| Go 漏洞扫描 | Go 1.26.8，server/agent/proto 的 Windows 与 Linux amd64 源码扫描：0 可达漏洞、0 已导入包漏洞 | 有 1 条仅模块层提示 GO-2026-5932：未使用的 x/crypto/openpgp，无上游修复版；禁止引入该包，见下文 |
| npm 漏洞扫描 | 锁文件修复后，生产依赖和全量 `npm audit --json` 均为 0；无 force 或主版本迁移 | 扫描时点结果；CI 保留生产高危门禁，后续升级继续复查 |
| 日志与 SQLite 维护 | logrotate 静态配置/50MiB fallback、隔离 SQLite 保留期清理、checkpoint 与 optimize 测试通过 | 未等待生产多日观察；logrotate 要有实际调度，WAL checkpoint 被长事务阻塞会每小时重试，均不是绝对文件大小上限 |
| 手机与最终页面 | 页面包含窄屏布局、次要列隐藏、全屏弹窗和自适应二维码；定向 TypeScript/lint 已通过 | 400px 浏览器全流程由主线验收，真实手机扫码与公网流程仍需实机执行 |

## 依赖提示的处置

Go 主版本保持 1.26，统一更新到官方已发布补丁 1.26.8；go.work、三个 go.mod、CI、Release 和 Docker 构建版本一致。旧 Go 1.26.2 扫描出现的 11 条可达标准库漏洞已在新工具链扫描中消失。锁文件将 axios 更新到 1.20.0、react-router 更新到 7.18.3，并更新相关开发依赖补丁。

[GO-2026-5932 官方报告](https://pkg.go.dev/vuln/GO-2026-5932) 将 x/crypto/openpgp 及其子包列为不安全、停止维护且没有修复版本。当前项目使用 x/crypto 的其他功能，没有导入或调用 openpgp，因此保留模块而不引入新的替代 OpenPGP 库；不能把该报告描述为“所有依赖模块零提示”。扫描采用 [Go 官方 govulncheck](https://go.dev/doc/tutorial/govulncheck) 的可达性结果。

## 持续运维

- 发布前执行 CI、摘要校验、Caddy access/error 脱敏用例，并抽查目标架构 Agent 自更新和回退。
- 备份数据库、JWT 主密钥与核心资产；按 runbook 演练恢复。JWT 主密钥变更前先禁用 TOTP。
- 定期确认 logrotate 调度、磁盘剩余空间、WAL checkpoint 警告与审计保留期，避免删除运行中 WAL 文件。
- 公网/真机未完成项保留为验收项，不因本地 fixture 通过自动关闭。
