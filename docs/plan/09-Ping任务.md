# 步骤 09 · Ping 任务

> 阶段 2 · 依赖：07 · 粗估 3 天 · 对应设计方案 §4.4、§5.2、§7.1 · 完成即里程碑 **M2**

## 1. 目标

后台能配置 ping 任务（三网延迟）并下发给 agent；agent 执行 ICMP/TCP 探测上报；卡片显示最近 30 次方块与丢包率，详情页有延迟曲线；设置页可管理任务。

## 2. 范围

**做**：`ping_tasks`/`ping_results` 表与 CRUD、`config` 下发与变更推送、agent 调度与执行、结果缓冲落库、内存最近窗口、recent/history API、卡片方块、详情曲线、设置页。

**不做**：告警（19）。

## 3. 前置条件

步骤 05（config 消息通道）、06（卡片）、08（详情页框架）。

## 4. 实现方案

### 4.1 迁移 `0004_ping.sql`

```sql
CREATE TABLE ping_tasks (
  id INTEGER PRIMARY KEY, name TEXT NOT NULL, target TEXT NOT NULL,
  kind TEXT NOT NULL DEFAULT 'icmp',            -- icmp | tcp（tcp 时 target 形如 host:port）
  interval_sec INTEGER NOT NULL DEFAULT 60,
  server_ids TEXT,                              -- JSON 数组；NULL = 全部节点
  enabled INTEGER NOT NULL DEFAULT 1, sort_order INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE ping_results (
  server_id INTEGER NOT NULL, task_id INTEGER NOT NULL, ts INTEGER NOT NULL,
  latency_ms REAL,                              -- NULL = 丢包
  PRIMARY KEY (server_id, task_id, ts)
) WITHOUT ROWID;
INSERT INTO ping_tasks (name, target, kind, interval_sec, sort_order) VALUES
 ('深圳电信', '202.96.134.33', 'icmp', 60, 1),
 ('深圳联通', '210.21.196.6', 'icmp', 60, 2),
 ('深圳移动', '211.136.192.6', 'icmp', 60, 3);
```

### 4.2 服务端 `server/internal/ping`

- `tasks.go`：CRUD；任何变更后调用 `hub.BroadcastConfig()`——对每个在线 agent 重新组装 `config`（只含作用于它的任务）并发送。
- `ingest.go`：hub 注册 `ping` 处理器 → 写入内存环 `recent[serverID][taskID]`（容量 30，元素 `{ts, latency}`）→ 追加到批量缓冲，每 5 秒一个事务落库。
- 快照：`ServerView.Ping` = 每任务 `{task_id, name, latency（最近一次，丢包为 null）, loss（窗口丢包率 %）}`；方块序列不进快照。
- API：
  - `GET /api/ping-tasks`、`POST`、`PUT /{id}`、`DELETE /{id}`（校验：icmp 目标为 IP 或域名不带端口；tcp 目标必须 `host:port`；interval 10–3600）
  - `GET /api/servers/{id}/ping/recent?n=30` → `{tasks:[{task_id,name,results:[{ts,latency}]}]}`（来自内存环，面板重启后从库回填）
  - `GET /api/servers/{id}/ping/history?task={id}&range=24h|7d|30d` → 分桶（24h 按分钟原始点；7d 按 10 分钟；30d 按小时）返回 `{ts, avg, max, loss}`，用 SQL `GROUP BY ts / bucket`
- 清理：rollup 任务里加 `DELETE FROM ping_results WHERE ts < now - retention.ping_days(默认 30)`。

### 4.3 Agent `agent/internal/ping`

```go
type Scheduler struct { mu; tasks map[int64]*runner; send func(proto.PingResult) }
func (s *Scheduler) Apply(tasks []proto.PingTask)   // 对齐：新增启动、变更重启、缺失停止
```

- 每任务一个 goroutine，`ticker(interval)`，首个执行随机延迟 0–interval 打散。
- ICMP：`pro-bing`，`SetPrivileged(true)`（root），`Count=1`、`Timeout=3s`；成功取 `AvgRtt` 毫秒（保留一位小数），超时上报 `latency_ms: null`。
- TCP：`net.DialTimeout("tcp", target, 3s)` 计时，连接成功即关闭。
- DNS 解析失败视为丢包并每 10 分钟 warn 一次。
- `transport` 收到 `config` → `scheduler.Apply(cfg.PingTasks)`。

### 4.4 前端

- 卡片 `ping-rows.tsx`：每任务一行：名称、`latency ms`（丢包显示 `超时`）、`loss %`、`PingBlocks`（30 个 `rect`，最新在右；着色：小于 100 绿、小于 200 黄、其余橙、null 红、无数据灰）。方块数据来自 `useSWR('/api/servers/{id}/ping/recent', {refreshInterval: 60000})`，最新值来自快照（每秒）。
- 详情页新增 Tab "延迟"：每任务一张图，延迟 avg 线 + max 细线 + 丢包率柱（右轴），range 与其它图联动。
- 设置页 `settings/ping-tasks`：`CustomDataGrid` + 表单 dialog（名称、类型、目标、间隔、作用节点多选、启用、排序）；保存后 toast "已下发到 N 台在线节点"。

## 5. 接口与数据

| 类型 | 内容 |
|---|---|
| 表 | `ping_tasks`、`ping_results` |
| WS | agent → server `ping`；server → agent `config.ping_tasks` 定稿 |
| REST | `/api/ping-tasks` CRUD、`/api/servers/{id}/ping/recent`、`/api/servers/{id}/ping/history` |
| 快照 | `servers[].ping[]` 填充 |
| settings | `retention.ping_days` |

## 6. 验收标准

- [ ] 默认三条任务下发后，各节点 1–2 分钟内卡片出现三行延迟，数值与手动 `ping` 同量级
- [ ] 新建 TCP 任务（如 `www.baidu.com:443`）后在线节点无需重启即开始上报
- [ ] 禁用任务后 agent 停止执行（日志），方块不再更新
- [ ] 拔掉某节点网络（或目标改为不可达 IP）→ 方块变红、loss 上升
- [ ] `ping_results` 每 5 秒批量写入；30 天前数据被清理
- [ ] 面板重启后 recent 接口仍返回最近 30 个点（从库回填）
- [ ] 详情页延迟曲线 24h / 7d / 30d 三档分桶正确
- [ ] 单测：环形缓冲、loss 计算、agent 调度对齐（新增/变更/删除）

## 7. 风险与注意

- 三网目标 IP 只是示例，以各节点实测为准（设计方案 §15 #4）；界面里给出"测试目标"按钮：向在线节点下发一次性探测并返回结果，可放二期。
- 部分 VPS 禁 ICMP 出站，改用 TCP 任务。
- 十几台 × 3 任务 × 每分钟 = 每天约 6 万行，30 天约 180 万行、约 60 MB，SQLite 无压力；间隔不要小于 30 秒。

## 8. 产出物

`0004_ping.sql`、`server/internal/ping/*`、`agent/internal/ping/*`、`web/src/sections/monitor/ping-rows.tsx`、`sections/servers/detail/ping-charts.tsx`、`sections/settings/ping-tasks/*`。

## 9. 偏离记录

（开工前为空）
