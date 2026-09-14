# 步骤 15 · 订阅用户与 Clash 订阅

> 阶段 3 · 依赖：13、14 · 粗估 4 天 · 对应设计方案 §4.6、§4.7、§10.1、§10.4 · 完成即里程碑 **M3**

## 1. 目标

前端能管理订阅用户（凭据、分配、额度字段）；服务端输出 Clash 与 clash-provider 格式订阅，带用量响应头；用 Clash 客户端扫码导入后四种协议都能连。

## 2. 范围

**做**：`proxy/sub` 的 clash / clash-provider 输出、订阅模板存储与默认值、`/sub/{token}` 路由（限速、缓存、日志脱敏）、订阅用户列表/表单/分配/详情页、订阅链接与二维码对话框。

**不做**：限额自动停用（16）、singbox/uri 格式与模板编辑 UI（17）。

## 3. 前置条件

步骤 13、14；一台节点四个入站已能应用。

## 4. 实现方案

### 4.1 订阅输出 `proxy/sub`

```go
type Proxy struct { Name, ServerName, Protocol string; Host string; Port int; Inbound model.Inbound; Sub store.Subscriber; Cert *store.Cert }
func Collect(db, sub) ([]Proxy, error)            // 该用户已分配、入站 enabled、节点 public_host 非空、节点非删除
func RenderClash(proxies []Proxy, tpl string) ([]byte, error)
func RenderClashProvider(proxies []Proxy) ([]byte, error)
```

- 命名 `{节点名} · {VLESS|SS|HY2|TUIC}`；同名冲突（同节点同协议多个入站）追加 `#端口`。
- 四协议字段按设计 §10.1；YAML 用 `yaml.v3` 的 `yaml.Node` 保证字段顺序；字符串值统一加引号避免 `yes/no/on` 类误判。
- 用户被停用（`enabled=false` 或 `auto_disabled != none`）时仍返回 200，但 `proxies` 为空数组并在文件首行注释 `# 已停用：{原因}`——客户端能刷新到"没节点"而不是报错。
- 模板：`settings.sub.clash_template`，默认值即设计 §10.1 的 proxy-groups/rules 段；占位 `{{PROXIES}}`（整段 `proxies:` YAML）与 `{{PROXY_NAMES}}`（逗号分隔、已加引号的名称列表）。渲染后再 `yaml.Unmarshal` 一次做语法校验，失败返回 500 并记日志（模板被改坏时能发现）。
- clash-provider：只输出 `proxies:`。

### 4.2 路由 `api/sub.go`

- `GET /sub/{token}` 与 `?format=clash|clash-provider`，无 JWT。
- token 查询：`subscribers.sub_token` 唯一索引直查；不存在 404，正文空。
- 响应头：`Content-Type: text/yaml; charset=utf-8`、`subscription-userinfo`（`upload=0; download={traffic_used}; total={limit}; expire={expire_unix}`——Clash 只区分上传下载之和，把 used 全放 download 最直观；不限额省略 total，无到期省略 expire）、`profile-update-interval: 24`、`content-disposition: attachment; filename*=UTF-8''{urlencode(name)}.yaml`、`Cache-Control: no-store`。
- 限速：每 token 每分钟 30 次，超出 429。
- 缓存：内存 10 s（key = token+format），用户/入站变更时全清。
- 访问日志：本路由不记录 URL 中的 token（记 `subscriber_id`）。Caddy 侧同步脱敏（步骤 07 预留）。

### 4.3 前端 `sections/subscribers/`

| 组件 | 内容 |
|---|---|
| `list/subscribers-list-view.tsx` | `CustomDataGrid`：名称、状态 `Label`（正常 / 手动停用 / 超额停用 / 到期停用——后两者步骤 16 才会出现）、用量（`QuotaBar`：已用 / 上限，不限额显示已用）、到期、分配节点数、操作（订阅、编辑、删除） |
| `quota-bar.tsx` | 细进度条 + 文本；颜色阈值 60/85% |
| `form/subscriber-form.tsx` | Tabs：基础（名称、备注、启用）；额度（上限 + 单位、重置日 0–31 下拉、到期日）；分配（`AssignmentPicker`）；凭据（编辑态只读展示 uuid / password / ss key，复制，"重新生成"确认） |
| `assignment-picker.tsx` | 按节点分组的复选树（`@mui/x-tree-view`）：节点行可全选/半选，子项为入站（协议 Label + 端口 + 备注）；禁用的入站灰显但可选 |
| `sub-links-dialog.tsx` | Tabs：Clash / Clash Provider（17 再加两项）；每项显示完整 URL、复制按钮、`QRCodeSVG`（`qrcode.react`）；底部"重置订阅 token"（确认：旧链接立刻失效） |
| `detail/subscriber-detail-view.tsx` | 顶部信息；按节点分账表（`subscriber_traffic` 当前账期）；最近 30 天用量柱图（`subscriber_traffic_daily`）；操作：清零用量、重置 token、重生凭据 |

`api/subscribers.ts`：SWR + mutations；订阅 URL 由 `CONFIG.serverUrl`（为空时 `location.origin`）+ `/sub/{token}?format=`。

### 4.4 服务端补充接口

- `GET /api/subscribers/{id}/traffic` → `{by_server:[{server_id, name, up, down}], daily:[{date, up, down}]}`
- 列表接口增加 `traffic_used / traffic_limit / expire_at / auto_disabled / servers_count`

### 4.5 端到端验证准备

`docs/verify/clash-import.md`：记录使用的 Clash 客户端与版本（Clash Verge Rev / CMFA）、四协议连通结果、`fingerprint` pin 是否生效（把指纹改错一位应连不上）、SS2022 `serverPSK:userPSK` 是否被接受、订阅页显示的用量与到期。

## 5. 接口与数据

| 类型 | 内容 |
|---|---|
| 公开 | `GET /sub/{token}?format=clash|clash-provider` |
| REST | `GET /api/subscribers/{id}/traffic`；列表字段增补 |
| settings | `sub.clash_template` |

## 6. 验收标准

- [ ] 新建订阅用户，分配一台节点的四个入站，节点 10 s 内应用（`users` 出现 `sub-N`）
- [ ] 订阅对话框二维码用 Clash Verge Rev 扫码导入成功，`proxies` 四条，命名正确
- [ ] 四条代理逐一切换均可访问外网；Hysteria2 / TUIC 在 `skip-cert-verify: false` 下连通；把 `fingerprint` 改错一位后连不上（pin 生效）
- [ ] Clash 订阅信息显示已用流量与到期（`subscription-userinfo` 生效）
- [ ] `format=clash-provider` 只有 `proxies:`；模板改坏时接口 500 且日志有明确错误
- [ ] 停用用户后刷新订阅得到空列表且首行注释说明；重置 token 后旧链接 404
- [ ] 同一 token 每分钟 31 次请求第 31 次 429
- [ ] 访问日志与 Caddy 日志里看不到 token 明文
- [ ] `docs/verify/clash-import.md` 记录完成

## 7. 风险与注意

- mihomo 各客户端版本对 `reality-opts`、`fingerprint` 字段支持略有差异，验证时锁定一个客户端版本写进文档。
- 用户没分配任何入站或节点没填 `public_host` 时代理列表为空，前端在保存时给出提示。
- 订阅 URL 本质是密码，前端只在对话框里展示，列表不展示。

## 8. 产出物

`server/internal/proxy/sub/*`、`api/sub.go`、`web/src/sections/subscribers/*`、`web/src/api/subscribers.ts`、`docs/verify/clash-import.md`、`protocol.md`。

## 9. 偏离记录

- 复用现有 MUI DataGrid（仓库没有 CustomDataGrid 封装），分配采用已安装的 MUI SimpleTreeView；不新增 Node 依赖。
- 缓存失效由 DB SubscriptionEpoch 统一驱动，成功代理事务和设置/节点写入递增；流量响应头每次读取。并发渲染仅在输入 epoch 未变化时写缓存。
- Caddy 对 /sub/* 跳过访问日志，应用记录 subscriber_id；证书和模板错误不回显包含秘密的数据。
- 真实 Clash 客户端扫码、连通、pin 反例和 Caddy 实际日志验收尚未执行，已明确列入 verify 文档。
