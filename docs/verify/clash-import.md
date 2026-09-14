# Clash 订阅验收

日期：2026-09-14。此记录区分自动化检查与真实客户端验收。

已实现并由定向测试覆盖：四协议 Clash YAML、特殊字符串与 IPv6、SS2022 `serverPSK:userPSK`、HY2/TUIC `fingerprint` 且 `skip-cert-verify: false`、模板语法/重复键/多文档拒绝、空 provider、匿名路由、实时用量头、节点地址/凭据/停用/token 的缓存更新、未知 token 空 404、渲染失败 500、日志不含 token/凭据、每分钟 30 次窗口边界、当前账期分账及 30 天补零。

命令（Linux Go 1.26）：`go test ./internal/proxy/sub ./internal/api ./internal/store -run TestSub -count=1` 与 `-run TestClash`；前端 `tsc --noEmit` 和本步骤文件 ESLint。

真实客户端尚未执行，不能据生成字段或单元测试推断连接成功：

| 项目 | 客户端与版本 | 结果 |
| --- | --- | --- |
| Clash Verge Rev/CMFA 扫码导入四协议 | 待填 | 待验收 |
| VLESS/SS/HY2/TUIC 分别访问外网 | 待填 | 待验收 |
| HY2/TUIC 正确指纹可连，改错一位断连 | 待填 | 待验收 |
| SS2022 多用户密码被接受 | 待填 | 待验收 |
| 客户端显示流量与到期 | 待填 | 待验收 |
| Caddy `adapt/validate` 和实际访问日志检查 | 待填 | 待验收 |

协议来源（2026-09-14 核对）：[mihomo TLS](https://wiki.metacubex.one/config/proxies/tls/)、[Hysteria2](https://wiki.metacubex.one/config/proxies/hysteria2/)、[TUIC](https://wiki.metacubex.one/config/proxies/tuic/)、[Shadowsocks](https://wiki.metacubex.one/config/proxies/ss/)。`fingerprint` 是完整证书的 SHA-256；叶子指纹匹配时不进行额外验证，不应称为公钥 pin 或通常 PKI 校验。`certificate` 在 mihomo TLS 配置中用于 mTLS，不应误当作信任根。

[Caddy log_skip](https://caddyserver.com/docs/caddyfile/directives/log_skip) 自 v2.8.0 使用此名称。配置对 `/sub/*` 跳过访问日志，并移除其它请求的 Referer 和 token 查询参数；应用日志仅记录脱敏路径和 subscriber_id。部署前需对当前 Caddy 版本执行配置校验。
