-- +goose Up
-- 节点级扫描状态：只有实例数组无法区分「等待首个快照」「扫描不完整」「完整扫描但确实没有实例」，
-- 这三种状态对"实例是否消失"的判断完全不同，所以按节点单独记一行。
CREATE TABLE proxy_observation_scans (
  server_id INTEGER PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
  complete INTEGER NOT NULL DEFAULT 0,
  collected_at INTEGER NOT NULL DEFAULT 0,
  received_at INTEGER NOT NULL DEFAULT 0,
  instances INTEGER NOT NULL DEFAULT 0
);

-- 历史记录要说明"何时确认消失"，保留期也按它计算。
--
-- 升级前已经标记 absent 的记录没有这个信息：received_at 是最后一次收到快照的时刻，
-- 不是首次确认缺失的时刻，不能拿它冒充。这里保留 0 表示未知，页面按"未记录"展示
-- 并另外给出最后观测时间；保留期对这些记录退回按 received_at 计算，不会无限保留。
ALTER TABLE proxy_observations ADD COLUMN absent_at INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE proxy_observations DROP COLUMN absent_at;
DROP TABLE proxy_observation_scans;
