# 步骤 10 · sing-box 构建与托管

> 阶段 3 · 依赖：07 · 粗估 2 天 · 对应设计方案 §9.1、§9.2、§6.6 corefiles、§15 #1 #2

## 1. 目标

有一条能产出带 `with_v2ray_api` 的 sing-box 二进制的流水线；面板能托管多个版本并指定"当前版本"；agent 能凭 token 下载；**在一台节点上手工验证每用户流量统计可用**，把结论写成文档。这是阶段 3 的地基。

## 2. 范围

**做**：`sing-box.yml` 工作流、corefiles 目录与 API、设置页版本管理、agent 下载接口、手工验证并记录。

**不做**：agent 自动安装（11）、渲染（13）。

## 3. 前置条件

步骤 07 上线；一台可以随便折腾的海外节点。

## 4. 实现方案

### 4.1 钉版本

开工时查 sing-box Releases 取最新稳定版（1.1x.y），记录到 `docs/core-version.md`：版本号、该版本文档的默认构建标签、changelog 中与我们相关的字段变更。渲染器（13）只针对这个版本写。

### 4.2 工作流 `.github/workflows/sing-box.yml`

- `workflow_dispatch` 输入 `version`（如 `v1.12.4`）
- 矩阵 `goarch: [amd64, arm64]`
- 步骤：checkout `SagerNet/sing-box` 指定 tag → `go build` 按设计 §9.2（标签 `with_quic,with_utls,with_v2ray_api`，若该版本文档仍列有 `with_reality_server` 则加上）→ 运行 `./sing-box version`，用 `grep -q with_v2ray_api` 失败即让流水线失败 → `sha256sum` → 上传 artifact，并创建本仓库 Release `sing-box-{version}` 附两架构文件与 `SHA256SUMS`
- 产物命名：`sing-box-{version}-linux-{arch}`

### 4.3 面板托管 `server/internal/corefiles`

- 目录：`{DataDir}/corefiles/{version}/sing-box-linux-{arch}` 与同目录 `SHA256SUMS`；`settings.core.current_version`。
- `Store` 接口：`List() []Version{version, arches[], uploaded_at, current}`、`Put(version, arch, reader, sha256)`（先写临时文件，校验 sha256 一致再落位）、`Delete(version)`（当前版本不可删）、`SetCurrent(version)`（两架构齐全才允许）、`Open(version, arch)`。
- 面板自身也需要一份与面板架构一致的二进制用于渲染预检（步骤 13）：`SetCurrent` 时把对应架构的文件复制为 `{DataDir}/corefiles/current-local`，`chmod 755`。distroless 镜像可直接执行静态二进制。
- API（JWT）：
  - `GET /api/corefiles`
  - `POST /api/corefiles`（multipart：`version`、`arch`、`file`、`sha256`；上限 64 MB）
  - `POST /api/corefiles/fetch`（可选：给一个 URL 让面板自己下载，方便从 GitHub Release 拉）
  - `PUT /api/corefiles/current { version }`
  - `DELETE /api/corefiles/{version}`
- API（agent token）：`GET /api/agent/corefiles/{version}/{arch}` → 文件流，`ETag: sha256`，`X-Checksum-Sha256` 头。复用步骤 05 的 token 校验中间件抽成 `auth.AgentMiddleware`。

### 4.4 设置页 `settings/corefiles`

版本表（版本、架构完整性、上传时间、当前标记）；"上传"对话框（选择版本号、两次上传两架构或一次多选）；"从 URL 拉取"；"设为当前"（确认："已安装节点不会自动升级，需在代理页逐台点升级"）；删除。

### 4.5 手工验证（必须做，写进 `docs/verify/sing-box-stats.md`）

在测试节点上：

1. 下载面板托管的二进制，`sing-box version` 确认 Tags 含 `with_v2ray_api`
2. 写一份最小配置：一个 `shadowsocks` 入站（2022 多用户，`users` 两个：`u1`、`u2`）+ `experimental.v2ray_api`（`stats.enabled=true`，`users:["u1","u2"]`，`inbounds:["ss-in"]`）+ direct 出站；`sing-box check -c` 通过；`sing-box run`
3. 本地用 sing-box 客户端（或 Clash）分别以 u1、u2 各下载 100 MB 文件
4. 用 `grpcurl`（或一个 20 行的 Go 程序，proto 来自 sing-box 仓库 `experimental/v2rayapi/stats.proto`）对 `127.0.0.1:10085` 调 `QueryStats{pattern:"", reset:true}`，确认出现 `user>>>u1>>>traffic>>>downlink ≈ 100MB`、`user>>>u2>>>...`；再调一次确认 `reset` 生效（归零）
5. 记录：实际版本、实际标签、计数器名称格式、误差、遇到的问题（例如 `Unimplemented` 是否出现）

验证不通过就在这一步解决（换版本、调标签），不要带着问题进步骤 11/13。

## 5. 接口与数据

| 类型 | 内容 |
|---|---|
| 文件 | `{DataDir}/corefiles/…` |
| settings | `core.current_version` |
| REST | `/api/corefiles*`（JWT）、`/api/agent/corefiles/{version}/{arch}`（agent token） |

## 6. 验收标准

- [ ] 工作流手动触发成功，Release 里有两架构文件与 SHA256SUMS；`version` 输出含 `with_v2ray_api`。**当前没有 remote，工作流与 Release 未触发。两架构已本地交叉编译，amd64 运行通过，ARM64 真机执行待补。**
- [x] 面板上传两架构、设为当前；`current-local` 可在 distroless 容器内执行 `version`
- [x] 用 agent token 下载接口拿到文件，sha256 与 SUMS 一致；错误 token 401（本轮 HTTP 客户端使用 PowerShell 与 Go）
- [x] 删除当前版本被拒绝；架构不全时"设为当前"被拒绝
- [x] `docs/verify/sing-box-stats.md` 写明验证结论（Linux amd64 容器通过），并附计数器原始输出；**海外节点验证待补**
- [x] `docs/core-version.md` 写明钉定版本与标签

## 7. 风险与注意

- 官方 release 不含 `with_v2ray_api`（设计方案已核实），别图省事直接拿官方包。
- 同时启用 `clash_api` 会让 v2ray_api 的统计失效（issue #2742），验证配置里不要带 `clash_api`。
- sing-box 用 Go 编译需要与其 go.mod 匹配的 Go 版本，工作流里用 `actions/setup-go` 读它的 `go.mod`。

## 8. 产出物

`.github/workflows/sing-box.yml`、`server/internal/corefiles/*`、`api/corefiles.go`、`auth.AgentMiddleware`、`web/src/sections/settings/corefiles/*`、`docs/core-version.md`、`docs/verify/sing-box-stats.md`。

## 9. 偏离记录

1. 核对最新稳定版后钉定 v1.14.0 与源码提交 0b8995879f29a9b98ee027bc17b75e101445b238。标签仍为 with_quic,with_utls,with_v2ray_api；没有 with_reality_server。
2. 工作流使用两个原生 runner，避免直接在 x86 上执行 ARM64 产物；两个架构都运行双用户统计验证才允许发布。Release 额外提供对应源码归档和许可证，重跑不覆盖既有 Release。
3. 增加 manifest.json 作为发布边界；二进制先写临时文件、流式校验 SHA256 / Go buildinfo / ELF 后发布。同版本同架构不可覆盖，相同 SHA256 重传幂等；单文件最多 64 MiB。
4. 使用 os.Root 约束目录访问，版本只接受稳定 tag 形式，架构只接受 amd64/arm64。上传不执行二进制；trimpath 产物不含 ldflags，实际版本号由工作流的 version 输出确认，不能声称上传端能证明版本标签与内部版本一致。
5. 当前版本持久化在 settings，current-local 是派生文件。启动根据设置重建，设为当前时验证两个文件完整性；写库失败不切换，替换失败回滚设置。支持 Windows 管理 Linux 构建，但本地执行预检只面向 Linux 部署。
6. 可选 URL 获取收窄到公开的 HTTPS GitHub Release 文件，仅允许 GitHub 及其 Release 资产域名重定向，不支持任意 URL 或私有仓库 Token。下载与上传共用校验，两路并发、两分钟时限。
7. 提取 auth.AgentMiddleware，WS 与下载端点共用现有 token 语义。下载支持 SHA256 ETag、校验和响应头和 Range；每次请求先鉴权，重置或删除节点后旧 token 失效。
8. 用独立 Go 验证程序代替 grpcurl，避免服务端反射依赖；真实 RPC 服务名是 v2ray.core.app.stats.command.StatsService，不能照抄 proto 的 package。工具独立于业务模块，原始结果保存在 docs/verify/。
9. 没有提供海外测试节点，本轮在网络隔离的 Linux amd64 容器中完成真实 SS2022 双用户各 100 MB 下载、用户隔离、入站汇总和 reset 验证。ARM64 执行和公网链路不在已验范围。

## 10. 本地验收记录（2026-09-14）

- 独立 distroless 面板绑定 127.0.0.1:19092，独立数据库，原有 8080/9000 服务未改动。
- 浏览器上传 amd64（40243362 字节）和 arm64（37486754 字节），单架构时按钮禁用，补齐后切换成功，当前版本删除按钮禁用；容器执行 current-local version 输出 1.14.0 与所需三个标签。
- Agent Token HTTP 下载 amd64：200，文件 SHA256 与原始构建及 X-Checksum-Sha256 一致；Go 集成测试验证未登录/JWT 冒充 Agent/错误与撤销 Token 的 401、Range 206、ETag 304。
- 存储测试验证损坏校验和、错误架构、缺标签、非 ELF、超限、取消、并发发布、不可覆盖、删除保护、设置写入失败、文件替换失败回滚、启动重建。
- 浏览器确认 400px 列表与表单无页面横向溢出；非 GitHub URL 显示来源限制错误；确认框关闭时不再闪出空版本文字。
- 停机后把测试 current-local 替换为无效内容，再启动面板：根据已选版本自动恢复，SHA256 与原始 amd64 一致，version 可执行，页面仍显示当前版本。
- Go vet / 三模块测试、独立统计工具 vet、npm test / tsc / ESLint / 生产构建通过；工作流 YAML 与 actionlint v1.7.7 静态检查通过。最终页面无新增浏览器错误；构建保留原有大 chunk 提示。
- 本轮未运行 CGO 竞态检查；原生 Windows 仍缺少相应 C 编译器。该限制与 ARM64、公网、远端工作流待验项分别记录。
- 双用户统计及 raw JSON 见 docs/verify/sing-box-stats.md；没有伪造海外或 ARM64 运行结果。
