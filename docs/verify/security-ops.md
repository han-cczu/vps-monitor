# 步骤 20 安全与运维验收

日期：2026-09-14。独立工作区 `step/20-security`；后端用临时 SQLite 和 httptest，未修改原服务或真实管理员凭据，未向 Telegram/邮件外发。此记录覆盖步骤 20 定向检查；主线负责合并后的全量 Go/前端构建、浏览器与 Linux 集成。

## 工具链与复验命令

Go 固定 1.26.8，govulncheck v1.8.0。Windows 官方压缩包 SHA-256：`b92c3b2adae85a11ba71fe7216daf0d84e82af4c8ab6c5625807f28622043a59`，已与 [Go 官方下载列表](https://go.dev/dl/) 核对。此次工具链和新构建缓存保存在 E 盘，未迁移已有模块/npm 缓存。

```powershell
# 在仓库根目录；下列工具路径为此次本地验收位置。
$env:Path='E:\agentlearn\vps-monitor-worktrees\step20\dist\tools\go1.26.8\go\bin;'+$env:Path
$env:GOTOOLCHAIN='local'
$env:GOMAXPROCS='2'
$env:GOCACHE='E:\agentlearn\vps-monitor-worktrees\step20\dist\go-cache'
go version
go test -p 1 ./server/internal/auth ./server/internal/store ./server/internal/api ./server/internal/maintenance ./server/cmd/server ./agent/internal/selfupdate ./agent/internal/corectl -run 'TestMFA|TestTOTP|TestSettings|TestMaintenance|TestResetTOTP|TestAgentUpdate|TestPublicAgentChecksum|TestUpdate|TestRejectMalformed|TestRedirect|TestLogrotate|TestFallback|TestSignInSuccessAndMe' -count=1
go test -p 1 ./server/internal/api -run 'TestProxyAPICRUDSecretsAndAuth|TestServerTokenReset' -count=1
go vet ./server/internal/api ./server/internal/auth ./server/internal/maintenance ./agent/internal/selfupdate
node web/node_modules/typescript/bin/tsc --project web/tsconfig.json --noEmit --incremental false
```

漏洞扫描须从模块目录执行，根目录只有 go.work，直接在根目录对 `./...` 扫描会报没有根模块：

```powershell
# cwd: server。Windows 默认平台扫描后，再临时设 GOOS/GOARCH 扫描 Linux。
..\dist\tools\govulncheck.exe -show verbose ./... ../agent/... ../proto/...
$env:GOOS='linux'
$env:GOARCH='amd64'
..\dist\tools\govulncheck.exe -show verbose ./... ../agent/... ../proto/...
Remove-Item Env:GOOS
Remove-Item Env:GOARCH
# cwd: web
npm audit --omit=dev --json
npm audit --json
```

定向 Go 测试、vet、TypeScript 与所改文件 ESLint/Prettier 检查均通过。最后的 MFA 数据库故障用例重复六次返回 500，不误记为密码/验证码失败、不产生账户锁定。

## 安全与接口用例

| 模块 | 通过的检查 |
|---|---|
| TOTP 算法/加密 | RFC 6238 SHA1 测试密钥在 59 秒的六位输出 287082；错误码、非数字、密文篡改、错误用户 AAD、错误主密钥拒绝 |
| MFA ticket | 五分钟过期、一次提交即消耗、同用户旧 ticket 作废；正确密码只返回 MFA 状态与 ticket；MFA 成功才签 JWT |
| 防暴力 | 用户/IP 维度限制，新鲜正确密码 ticket 不清零 MFA 错误累计；管理操作使用独立限制 |
| 防重放 | 8 个并发消费者仅 1 成功；SQLite 关闭再打开仍拒绝相同步；同样数字在 90 秒内即使不同 step 也拒绝 |
| 凭据竞争 | 读取后修改 password_hash、secret、enabled 的确定性错配均失败；改密码后旧 pending 不能启用；消费、启用/禁用和审计事务回滚 |
| 恢复 CLI | 隔离数据目录执行 reset-totp 后可密码登录，保留密码、写系统审计、不输出秘密 |
| 设置 | 白名单和边界值校验，部分更新保留内部未知配置；订阅模板缺少验证器时拒绝；触发器模拟审计失败后设置不提交 |
| 审计 | 分页/操作者/动作/时间筛选；14 种代理写操作均有记录并检查凭据/私钥脱敏；TOTP 与模板不记原文 |
| Agent 更新 | SHA-256、`--version`、架构、固定路径、消息 2048 字节、产物 64MiB、60 秒下载和 5 秒执行限制；错误版本/下载错误/重定向/取消/重启失败/降级拒绝并保留旧文件 |
| 公开产物 | 两个固定 Linux 二进制、对应两个固定摘要、VERSION 与安装脚本的 allowlist；任意文件摘要路径和非普通文件拒绝 |
| 维护 | 审计超期删除且保留近期；TOTP 旧使用码清理；WAL TRUNCATE、optimize；logrotate 配置与缺少 logrotate 时的超大文件 fallback |

审计用例覆盖 `inbound.create/update/regenerate_keys/delete`、`cert.generate/regenerate`、`subscriber.create/update/regenerate_credentials/reset_token/reset_usage/delete`、`assignment.update`、`node_advanced.update`，共 14 类。

## 依赖结果

修复前 Go 1.26.2 扫描发现 11 条可达标准库漏洞，npm 生产依赖存在高危。修复后 Go 1.26.8 的 Windows/Linux amd64 三模块源码扫描均为 0 可达漏洞、0 已导入包漏洞；仍有一个模块层报告 [GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932)，属于项目未导入的 x/crypto/openpgp，无上游修复版。

工作区实际选中 grpc 1.83.2、x/net 0.58.0、x/text 0.42.0、protobuf 1.36.12、x/crypto 0.57.0、goose 3.28.0。npm 锁文件 axios 1.20.0、react-router 7.18.3；生产与全部依赖扫描均 0 漏洞。仅更新锁文件，没有对共用 node_modules 执行 npm ci。

合并后复查：从主线提交 `fef511005c8ba99d40d9333ef828961e826b2bbb` 导出独立源码快照，使用同一工具链/scanner 对三个模块的 34 个根包分别运行 Windows amd64 和 Linux amd64 扫描，两者退出码均为 0：0 可达、0 已导入包漏洞，仍仅有上述未使用 openpgp 的模块提示。该次包含步骤 15/17 新增订阅导入，没有修改主线工作区。原始输出保存在本次本地安全工作区 `dist/govulncheck-fef5110-windows.txt`、`dist/govulncheck-fef5110-linux.txt`，未提交忽略目录。

## 主线真实集成证据与未验边界

- Linux amd64/systemd：主线 `dist/step15-20-qa/smoke.json` 记录实际 Agent v0.1.0→v0.1.1，目标摘要与 `.bak` 旧摘要匹配，PID 保持，重新连接和版本回报耗时 3.339 秒；这是同一代码改变版本号的测试构建，初轮使用旧 Go 1.26.2。正式补丁工具链的最终重建由主线另行记录，不能用此条声称已发布新镜像/远端 Release。
- Caddy：主线提交 b93c754 的 `ci/caddy-smoke.py` 在真实 Docker Caddy v2.11.4 验证 access.log 和默认 error logger；正常订阅及故意上游 502 场景中的路径 token、查询 token 和 Referer 测试值均被移除，配置 validate 通过，自建测试容器已清理。首次只过滤 access logger 发现 502 泄漏，最终配置增加全局过滤后复验通过。
- 标题由顶栏消费；单位由 `formatBytes/formatRate` 消费并重新呈现；timezone setter 影响 `daysUntil/formatPanelDate`；retention 分别由 metrics、ping、maintenance 清理任务读取。订阅统计模式和冷却由 16/19 的运行消费者接入，不重算历史。400px 全流程、真正浏览器切换单位/时区由主线最终验收补充。
- 未在此分支执行：公网 HTTPS/WSS 与证书更新、云防火墙、ARM64 真机更新、真实手机扫码、多日 logrotate/WAL 运行观测、真实发布镜像更新。错误 TLS pin 的真实负向测试由主线补齐。不得将这些项勾为已完成。

## 实现依据

最终补充：Go1.26.8重新构建后的27项真实集成通过，Agent同PID更新重连3.311秒；HY2/TUIC错误pin均拒绝。浏览器已验证400px订阅/规则/弹窗、二步登录、单位/时区/标题保存与审计过滤；Linux全模块race通过。详见 [completion.md](completion.md)，保留本文件以上初轮时间与版本作为历史证据。

- TOTP 使用 [pquerna/otp](https://github.com/pquerna/otp) 与 [RFC 6238](https://datatracker.ietf.org/doc/html/rfc6238)；库校验之外增加数据库持久去重和凭据 CAS。
- 维护采用 [SQLite PRAGMA 文档](https://www.sqlite.org/pragma.html) 的 checkpoint/optimize；checkpoint busy 保留下一小时重试。
- 日志过滤按 [Caddy log 官方文档](https://caddyserver.com/docs/caddyfile/directives/log) 配置，并以实际容器正常/错误响应验证。
- 漏洞处置依据 [govulncheck 官方说明](https://go.dev/doc/tutorial/govulncheck) 和上游安全公告，结果按扫描时点记录。
