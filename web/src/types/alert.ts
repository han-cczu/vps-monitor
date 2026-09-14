export type AlertRule = {
  kind: string;
  enabled: boolean;
  updated_at: number;
  params: { minutes?: number; percent?: number; percents?: number[]; days?: number[] };
};

export type NotifyChannel = {
  id: number;
  name: string;
  kind: 'telegram' | 'webhook';
  enabled: boolean;
  created_at: number;
  config: { chat_id?: string; url?: string; has_bot_token?: boolean; has_secret?: boolean };
};

export type AlertEvent = {
  id: number;
  rule_kind: string;
  target_type: 'server' | 'subscriber';
  target_id: number;
  level: 'info' | 'warning' | 'critical';
  title: string;
  message: string;
  fired_at: number;
  resolved_at: number | null;
  notified_at: number | null;
  dedupe_key: string;
};

export type AlertEventsData = {
  events: AlertEvent[];
  total: number;
  open_count: number;
  page: number;
  page_size: number;
};
