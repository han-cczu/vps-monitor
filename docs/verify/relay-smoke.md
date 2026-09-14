# 步骤 17：本地真实两跳 relay 冒烟脚本

入口：在原 `ci/reconcile-smoke.py` 命令中增加 `--relay`，无需 `--mihomo`，也可与 `--mihomo`、`--agent-next` 组合。复用原脚本创建的临时面板、Agent、受管核心 A、普通订阅用户和 SS2022 本地客户端。

原脚本仍要求空闲 Linux/systemd 验收环境和 root，拒绝已有受管核心路径/服务。新增 `ci/relay_smoke.py` 只通过传入的 API 操作临时面板、运行独立 B 子进程，不自行调用 systemd 或修改核心安装路径。

## 运行时验证链

1. 通过现有客户端请求本地 `/small`，校验完整 64 KiB 内容，建立 A direct 基线。
2. 新建无 Agent 的临时节点 B 和独立 SS2022 入站，用 `POST /api/servers/{A}/advanced/relay` 生成专属 relay 用户及 A 出站。校验凭据独立于普通用户、仅分配 B 入站、默认用户列表不显示 relay。
3. 对离线 B 调用 `POST .../core/apply`，取返回修订号对应的 `GET .../core/revisions/{rev}` 中 `config_json`。离线节点能生成期望修订，无需伪造 Agent 在线或 applied 状态。
4. 保留 B 生成配置的协议、ACL、凭据与路由，仅删除 `experimental`（避免与 A 的 `127.0.0.1:10085` 冲突）及将 B 入站绑定至 `127.0.0.1`。真实核心先 `check` 后作为独立子进程运行。
5. 等待 A 的 managed relay 修订实际应用，通过既有客户端请求 A → B → 本地 payload，校验完整内容。
6. 停止 B 后，同一路径必须失败；重启 B 后必须恢复。这一对照用于排除 A 绕过 B 直接访问本地目标。
7. DELETE relay，等待 A 新修订的 `route.final=direct` 且出站中已移除 helper tag；确认 relay 历史用户保留但分配为空，B 新期望配置为空 managed ACL。停着 B 再请求 A，验证 direct 实际恢复。

每次 curl 都是新进程/新代理连接。临时配置权限为 0600。证据只包含布尔断言、修订号、负载长度和 SHA-256，不输出节点 token、订阅 token、SS 密钥或配置正文。出错时尝试停止 B 并移除 relay，外层脚本继续负责其全部临时进程/服务清理。

## 验证边界

- 本次提交仅执行 Python 语法解析、`--help` 入口检查，以及配置隔离函数的纯内存正负断言；**未运行真实核心或重型集成**。两跳网络结果须由主任务在完整冒烟成功后记录，不能将脚本内的预期断言当作已通过证据。
- B 无 Agent；删除了统计扩展，因此此脚本不验证 B 的 Agent 上报、流量计费、双节点分账或重启对齐。证据明确包含 `target_agent=false`、`target_accounting_verified=false`。
- 只验证 SS2022 的 A→B TCP 转发及 relay 移除；不代表 VLESS 中转、UDP、公网跨主机、真实出口 IP 或高负载表现。
- 删除 relay 后检查 B 的新期望 ACL，但没有把该新配置应用到 B 再测旧凭据拒绝；B 在 direct 恢复验证时已经停止。
