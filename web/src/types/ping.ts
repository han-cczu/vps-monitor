export type PingTask = {
  id: number;
  name: string;
  target: string;
  kind: 'icmp' | 'tcp';
  interval_sec: number;
  server_ids: number[] | null;
  enabled: boolean;
  sort_order: number;
  created_at: number;
  updated_at: number;
};
export type PingTaskPayload = Omit<PingTask, 'id' | 'created_at' | 'updated_at'>;
export type PingSummary = {
  task_id: number;
  name: string;
  latency: number | null;
  loss: number;
  last_ts: number | null;
};
export type PingPoint = { ts: number; latency: number | null };
export type PingRecentTask = { task_id: number; name: string; results: PingPoint[] };
export type PingRange = '1h' | '24h' | '7d' | '30d';
export type PingHistory = {
  step: number;
  from: number;
  to: number;
  task_id: number;
  name: string;
  points: { ts: number; avg: number | null; max: number | null; loss: number }[];
};
