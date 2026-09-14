import type { ProxyAssignment } from './proxy';

export type Subscriber = ProxyAssignment & {
  name: string;
  note: string;
  enabled: boolean;
  auto_disabled: 'none' | 'quota' | 'expired';
  status?: 'active' | 'disabled' | 'quota' | 'expired';
  sub_token?: string;
  uuid?: string;
  password?: string;
  ss_user_key?: string;
  traffic_limit: number;
  traffic_used: number;
  reset_day: number;
  next_reset_date?: string | null;
  period_start: number;
  expire_at: string | null;
  servers_count: number;
};
export type SubscriberPayload = Pick<
  Subscriber,
  'name' | 'note' | 'enabled' | 'traffic_limit' | 'reset_day' | 'expire_at'
>;
export type SubscriberTraffic = {
  by_server: { server_id: number; name: string; up: number; down: number }[];
  daily: { date: string; up: number; down: number }[];
};
