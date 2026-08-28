// TypeScript interfaces mirroring the Go gateway JSON models.

export interface Carrier {
  id: number
  name: string
  type: string
  uuid?: string
  profile_id?: string
}

export interface CarrierCreateRequest {
  name: string
  type: string
  username: string
  password: string
  sms_limit?: number
  mms_limit?: number
}

export interface NumberSettings {
  id?: number
  number_id?: number
  sms_burst_limit: number
  sms_daily_limit: number
  sms_monthly_limit: number
  mms_burst_limit: number
  mms_daily_limit: number
  mms_monthly_limit: number
  limit_both: boolean
  auto_reply_enabled: boolean
  auto_reply_message: string
  auto_reply_cooldown_secs: number
}

export interface ClientNumber {
  id: number
  client_id: number
  number: string
  carrier: string
  tag: string
  group: string
  ignore_stop_cmd_sending: boolean
  webhook: string
  settings?: NumberSettings | null
}

export interface ClientSettings {
  id?: number
  client_id?: number
  auth_method: string
  api_format: string
  disable_message_splitting: boolean
  webhook_retries: number
  webhook_timeout_secs: number
  include_raw_segments: boolean
  default_webhook: string
  sms_burst_limit: number
  sms_daily_limit: number
  sms_monthly_limit: number
  mms_burst_limit: number
  mms_daily_limit: number
  mms_monthly_limit: number
  limit_both: boolean
}

export interface Client {
  id: number
  username: string
  name: string
  address: string
  type: string
  timezone: string
  log_privacy: boolean
  settings?: ClientSettings | null
  numbers: ClientNumber[]
}

export interface ClientCreateRequest {
  username: string
  password: string
  name: string
  type: string
  address?: string
}

export interface ClientUpdateRequest {
  name?: string
  type?: string
  address?: string
}

export interface ClientSettingsUpdate {
  auth_method?: string
  api_format?: string
  disable_message_splitting?: boolean
  webhook_retries?: number
  webhook_timeout_secs?: number
  include_raw_segments?: boolean
  default_webhook?: string
  sms_burst_limit?: number
  sms_daily_limit?: number
  sms_monthly_limit?: number
  mms_burst_limit?: number
  mms_daily_limit?: number
  mms_monthly_limit?: number
  limit_both?: boolean
}

export interface NumberUpdateRequest {
  carrier?: string
  tag?: string
  group?: string
  webhook?: string
  ignore_stop_cmd_sending?: boolean
  auto_reply_enabled?: boolean
  auto_reply_message?: string
  auto_reply_cooldown_secs?: number
}

export interface AutoReplyConfig {
  number: string
  number_id: number
  master_enabled: boolean
  enabled: boolean
  effective_enabled: boolean
  suppressed_by_stop: boolean
  message: string
  effective_message: string
  cooldown_secs: number
  env_default_fallback?: string
}

export interface AutoReplyUpdate {
  enabled?: boolean
  message?: string
  cooldown_secs?: number
}

export interface ApiKeyNumber {
  id: number
  api_key_id: number
  client_number_id: number
  number?: string
}

export interface TenantApiKey {
  id: number
  client_id?: number
  name: string
  key_prefix: string
  scopes: string
  rate_limit: number
  active: boolean
  expires_at?: string | null
  last_used_at?: string | null
  created_at?: string
  allowed_numbers?: ApiKeyNumber[]
}

export interface ApiKeyCreateRequest {
  name: string
  scopes: string
  rate_limit: number
  expires_in_days: number
  allowed_number_ids: number[]
}

export interface ApiKeyCreateResponse extends TenantApiKey {
  key: string
}

export interface Failover {
  id: number
  primary_client_id?: number
  fallback_client_id: number
  fallback_client_name?: string
  fallback_client_username?: string
  priority: number
  enabled: boolean
  fallback_online?: boolean
}

export interface LegacyStatus {
  online: boolean
  ip?: string
  failovers?: { username: string; name: string; priority: number; online: boolean }[]
  mm4?: {
    online: boolean
    active_sessions: number
    first_connect_at: string
    last_activity_at: string
  }
}

export interface StatsResponse {
  smpp_connected_clients: number
  smpp_clients: { username: string; ip_address: string; last_seen: string }[]
  mm4_connected_clients: number
  mm4_clients: {
    client_id: string
    username: string
    active_sessions: number
    first_connect_at: string
    last_activity_at: string
  }[]
}
