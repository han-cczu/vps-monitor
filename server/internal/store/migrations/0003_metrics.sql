-- +goose Up
-- 步骤 08：历史指标。秒级上报不落库，先在内存里按分钟聚合（metrics_minute），
-- 再每小时降采样成 metrics_hour；分钟表默认留 7 天、小时表留 365 天。
--
-- 两张表都用 WITHOUT ROWID + (server_id, ts) 复合主键：按节点取一段时间的查询
-- 直接走主键顺序扫描，不需要再建二级索引（再建反而多一份写放大）。
-- ts 是区间起点的 Unix 秒：分钟表是整分，小时表是整点。

CREATE TABLE metrics_minute (
  server_id   INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  ts          INTEGER NOT NULL,           -- 分钟起点（Unix 秒，能被 60 整除）

  cpu_avg     REAL    NOT NULL DEFAULT 0, -- 0–100
  cpu_max     REAL    NOT NULL DEFAULT 0,
  mem_used    INTEGER NOT NULL DEFAULT 0, -- 字节，区间平均
  swap_used   INTEGER NOT NULL DEFAULT 0,
  disk_used   INTEGER NOT NULL DEFAULT 0, -- 取区间内最后一条：磁盘是水位不是速率，平均没有意义
  load1       REAL    NOT NULL DEFAULT 0,
  load5       REAL    NOT NULL DEFAULT 0,
  load15      REAL    NOT NULL DEFAULT 0,

  rx_rate_avg INTEGER NOT NULL DEFAULT 0, -- 字节/秒
  rx_rate_max INTEGER NOT NULL DEFAULT 0,
  tx_rate_avg INTEGER NOT NULL DEFAULT 0,
  tx_rate_max INTEGER NOT NULL DEFAULT 0,
  rx_total    INTEGER NOT NULL DEFAULT 0, -- 累计字节，取最后一条（步骤 18 的结算按它算差值）
  tx_total    INTEGER NOT NULL DEFAULT 0,

  tcp         INTEGER NOT NULL DEFAULT 0, -- 取最后一条
  udp         INTEGER NOT NULL DEFAULT 0,
  procs       INTEGER NOT NULL DEFAULT 0,

  samples     INTEGER NOT NULL,           -- 这一分钟实际收到多少条 metrics，正常接近 60

  PRIMARY KEY (server_id, ts)
) WITHOUT ROWID;

CREATE TABLE metrics_hour (
  server_id   INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  ts          INTEGER NOT NULL,           -- 整点（Unix 秒，能被 3600 整除）

  cpu_avg     REAL    NOT NULL DEFAULT 0,
  cpu_max     REAL    NOT NULL DEFAULT 0,
  mem_used    INTEGER NOT NULL DEFAULT 0,
  swap_used   INTEGER NOT NULL DEFAULT 0,
  disk_used   INTEGER NOT NULL DEFAULT 0,
  load1       REAL    NOT NULL DEFAULT 0,
  load5       REAL    NOT NULL DEFAULT 0,
  load15      REAL    NOT NULL DEFAULT 0,

  rx_rate_avg INTEGER NOT NULL DEFAULT 0,
  rx_rate_max INTEGER NOT NULL DEFAULT 0,
  tx_rate_avg INTEGER NOT NULL DEFAULT 0,
  tx_rate_max INTEGER NOT NULL DEFAULT 0,
  rx_total    INTEGER NOT NULL DEFAULT 0,
  tx_total    INTEGER NOT NULL DEFAULT 0,

  tcp         INTEGER NOT NULL DEFAULT 0,
  udp         INTEGER NOT NULL DEFAULT 0,
  procs       INTEGER NOT NULL DEFAULT 0,

  samples     INTEGER NOT NULL,           -- 这一小时由多少个分钟行汇总而来，正常接近 60

  PRIMARY KEY (server_id, ts)
) WITHOUT ROWID;

-- +goose Down
DROP TABLE IF EXISTS metrics_hour;
DROP TABLE IF EXISTS metrics_minute;
