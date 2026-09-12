-- +goose Up
-- 步骤 03：节点。servers 是面板里手工维护的配置，server_host_info 是 agent 上报的静态信息（步骤 05 起写入）。

CREATE TABLE servers (
  id                INTEGER PRIMARY KEY,
  name              TEXT    NOT NULL,
  region            TEXT    NOT NULL DEFAULT '',   -- ISO 3166-1 alpha-2，空表示未填
  group_name        TEXT    NOT NULL DEFAULT '',
  tags              TEXT    NOT NULL DEFAULT '[]', -- JSON 字符串数组
  sort_order        INTEGER NOT NULL DEFAULT 0,
  token_hash        TEXT    NOT NULL UNIQUE,       -- agent token 的 sha256 hex，明文只在创建/重置时返回一次
  public_host       TEXT    NOT NULL DEFAULT '',   -- 订阅里使用的 IP 或域名
  price             REAL    NOT NULL DEFAULT 0,
  currency          TEXT    NOT NULL DEFAULT 'CNY',
  billing_cycle     TEXT    NOT NULL DEFAULT 'month', -- month | quarter | year | once
  expire_at         TEXT,                             -- YYYY-MM-DD，NULL 表示不设到期
  auto_renew        INTEGER NOT NULL DEFAULT 0,
  traffic_limit     INTEGER NOT NULL DEFAULT 0,       -- 字节，0 = 不限
  traffic_reset_day INTEGER NOT NULL DEFAULT 1,       -- 每月第几天重置，1–31
  traffic_mode      TEXT    NOT NULL DEFAULT 'max',   -- out | in | sum | max
  bandwidth_label   TEXT    NOT NULL DEFAULT '',
  note              TEXT    NOT NULL DEFAULT '',
  created_at        INTEGER NOT NULL,
  updated_at        INTEGER NOT NULL
);

-- 列表默认按 sort_order, id 排
CREATE INDEX idx_servers_sort ON servers(sort_order, id);

CREATE TABLE server_host_info (
  server_id     INTEGER PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
  hostname      TEXT,
  os            TEXT,
  kernel        TEXT,
  arch          TEXT,
  cpu_model     TEXT,
  cores         INTEGER,
  mem_total     INTEGER,
  disk_total    INTEGER,
  boot_time     INTEGER,
  ipv4          INTEGER,  -- agent 探测到的 IPv4 可达性
  ipv6          INTEGER,
  public_ip     TEXT,     -- 服务端从 agent 连接地址记下的公网 IP（步骤 05）
  agent_version TEXT,
  updated_at    INTEGER
);

-- +goose Down
DROP TABLE IF EXISTS server_host_info;
DROP INDEX IF EXISTS idx_servers_sort;
DROP TABLE IF EXISTS servers;
