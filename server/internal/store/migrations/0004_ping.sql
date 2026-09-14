-- +goose Up
-- 步骤 09：ping 任务与结果。
--
-- ping_tasks 是面板里配的探测目标，通过 config 消息下发给 agent；
-- ping_results 是 agent 回报的每次探测结果，latency_ms 为 NULL 表示这次超时/丢包。
--
-- 结果表用 WITHOUT ROWID + (server_id, task_id, ts) 复合主键：
-- 查询永远是「某台节点的某个任务在某段时间」，正好顺着主键扫。
-- 十几台 × 3 任务 × 每分钟一次 ≈ 每天 6 万行，留 30 天约 180 万行，SQLite 毫无压力。

CREATE TABLE ping_tasks (
  id           INTEGER PRIMARY KEY AUTOINCREMENT, -- 不复用任务 ID，避免旧任务在途结果写到新任务
  name         TEXT    NOT NULL,
  target       TEXT    NOT NULL,                 -- icmp: IP 或域名；tcp: host:port
  kind         TEXT    NOT NULL DEFAULT 'icmp',  -- icmp | tcp
  interval_sec INTEGER NOT NULL DEFAULT 60,
  server_ids   TEXT,                             -- JSON 数组；NULL = 作用于全部节点
  enabled      INTEGER NOT NULL DEFAULT 1,
  sort_order   INTEGER NOT NULL DEFAULT 0,
  created_at   INTEGER NOT NULL DEFAULT 0,
  updated_at   INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE ping_results (
  server_id  INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  task_id    INTEGER NOT NULL REFERENCES ping_tasks(id) ON DELETE CASCADE,
  ts         INTEGER NOT NULL,   -- 服务端收到的时刻（Unix 秒）
  latency_ms REAL,               -- NULL = 丢包

  PRIMARY KEY (server_id, task_id, ts)
) WITHOUT ROWID;

-- 三网延迟的默认目标。这几个 IP 只是常见的示例（设计方案 §15 #4），
-- 实际要以各节点 ping 出来的结果为准，面板里可以随时改。
INSERT INTO ping_tasks (name, target, kind, interval_sec, sort_order, created_at, updated_at) VALUES
  ('电信', '202.96.134.33', 'icmp', 60, 1, unixepoch(), unixepoch()),
  ('联通', '210.21.196.6',  'icmp', 60, 2, unixepoch(), unixepoch()),
  ('移动', '211.136.192.6', 'icmp', 60, 3, unixepoch(), unixepoch());

-- +goose Down
DROP TABLE IF EXISTS ping_results;
DROP TABLE IF EXISTS ping_tasks;
