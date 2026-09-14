# 订阅格式、高级 JSON 与中转验收

日期：2026-09-14。

已执行：

- `VM_TEST_SINGBOX=.../sing-box-v1.14.0-linux-amd64 go test ./internal/proxy/sub -count=1`：四个 sing-box 出站以真实 sing-box 1.14.0 `check` 校验通过。HY2/TUIC 使用内嵌 PEM 信任证书和 `insecure:false`；不是把 mihomo 完整证书指纹误填到公钥 pin 字段。
- URI 四协议 round-trip：标准 Base64 外层、IPv6、UTF-8/引号/换行名称、SS2022 组合密码 percent-encoding、HY2 `pinSHA256`、TUIC `allow_insecure=1`，空 URI 与空 outbounds。
- 高级 JSON：预检失败不保存、只检查不创建修订、检查期间配置变化返回冲突、保存触发通知；原始诊断保留字段错误文本并脱敏密码/UUID/私钥/证书，诊断限 16 KiB，截断时丢弃不完整末行。
- 中转：失败无用户残留、重复调用复用用户、源/目标节点都通知；移除当前默认中转后解绑对应用户，历史流量保留。常规用户列表隐藏 relay，节点分账可显示 relay。
- 中转身份、分配及凭据只能由助手修改，拒绝常规用户接口单独旋转凭据或更名，防止源节点出站继续持有失效凭据；仍允许清零用量。
- `VM_TEST_SINGBOX=... go test ./internal/proxy -run TestRelay`：真实核心对生成中转、移除后配置均预检通过；高级未知字段被真实核心拒绝，错误返回中保留字段名。
- HTTP 鉴权、UTC+8 面板/UTC 系统跨日（订阅到期头与 30 天分账）、既有代理 CRUD 回归、前端 TypeScript 和 ESLint。

未在本步骤工作区执行的网络验收：sing-box 四出站实际传输、Shadowrocket/NekoBox URI 导入、双节点 A→B 出口 IP 变化及移除后恢复、云安全组实际端口检查。由主任务集成环境完成，不能由 `check` 推断通过。

主要来源（2026-09-14 核对）：

- [sing-box VLESS](https://sing-box.sagernet.org/configuration/outbound/vless/)、[Hysteria2](https://sing-box.sagernet.org/configuration/outbound/hysteria2/)、[TUIC](https://sing-box.sagernet.org/configuration/outbound/tuic/)、[TLS](https://sing-box.sagernet.org/configuration/shared/tls/)：客户端 outbounds 及 PEM 行数组；`certificate_public_key_sha256` 为公钥 SHA-256 Base64，与证书整张指纹不同。
- [Shadowsocks SIP002](https://shadowsocks.org/doc/sip002.html)：AEAD-2022 userinfo 不得使用 Base64URL，method/password 必须 percent-encode。
- [Hysteria URI](https://hysteria.network/docs/developers/URI-Scheme/)：`pinSHA256` 为证书指纹。自签证书 URI 用 `insecure=1` 加 pin；客户端若忽略 pin 将无法提供同等验证，所以推荐 Clash/sing-box 格式。
- [TUIC 协议](https://github.com/tuic-protocol/tuic)：协议库没有唯一官方实现；URI 客户端选项存在差异。当前兼容格式不附带 pin，UI 明示 TUIC URI 无证书校验。

高级编辑器要求当前托管版本在服务端可执行。核心缺失或平台不可执行时明确拒绝保存；没有把“未跑 check”当作通过。默认模板留空保存会恢复服务端默认；通用 `/api/settings` 由步骤 20 实现，集成时通过 Deps.ValidateSetting 接 `sub.ValidateClashTemplate`，SettingsChanged 清订阅缓存。
