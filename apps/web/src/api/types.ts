export type Role = 'ADMIN' | 'OPERATOR' | 'VIEWER'
export type Quality = 'valid' | 'invalid' | 'missing' | 'outdated'
export interface Reading { value: number | boolean | null; quality: Quality; unit?: string }
export interface Snapshot {
  effective_at_utc: string; received_at_utc: string; time_quality: string
  age_seconds: number; outdated: boolean; data_source: string; readings: Record<string, Reading>
}
export interface Unit {
  id: string; name: string; device_id: string; data_source: string; is_simulated: boolean
  connection: 'ONLINE' | 'STALE' | 'OFFLINE' | 'NEVER_SEEN'
  last_state_at: string | null; control_state: string | null; control_state_note?: string
  pump_commanded: string | null; water_delivery: string | null
  applied_program_version: number | null; essential_interlocks_ok: boolean | null
  fault_codes: string[]; firmware_version: string | null
  controller_buffer: { occupancy: number; capacity: number; unreceipted: number; overflow_count: number } | null
  active_incidents: Record<string, number>; latest_readings: Snapshot | null
}
export interface Program {
  mode: 'TIMED_CYCLE'; on_duration_seconds: number; off_duration_seconds: number
  max_continuous_on_seconds: number; require_reservoir_ok: true
}
export type CommandStatus = 'PENDING' | 'SENT' | 'APPLIED' | 'REJECTED' | 'CONFLICT' | 'EXPIRED' | 'UNKNOWN'
export interface Command {
  id: string; unit_id: string; status: CommandStatus; reason_code: string | null
  expected_program_version: number; new_program_version: number | null
  attempts: number; applied_confirmed: boolean; note: string; program?: Program
}
export interface Incident {
  id: string; unit_id: string; severity: 'info' | 'warning' | 'critical'; code: string; status: string
  title: string; cause: string; suggested_action: string
  first_seen_at: string; last_seen_at: string; occurrences: number
  acknowledged_by: string | null; acknowledged_at: string | null; resolved_at: string | null
}
export interface Session {
  user: { id: string; username: string; role: Role }; source: 'local'
  capabilities: string[]; site: { id: string; name: string }
  simulated_data_accepted: boolean; server_time: string
}
export interface Summary {
  units: { total: number; healthy: number; attention: number; stale: number; never_seen: number }
  active_incidents: { critical: number; warning: number; total: number }
  contains_simulated_data: boolean
  connectivity: { mqtt_broker_connected: boolean; site_wan_reachable: boolean | null; cloud_api_reachable: boolean | null; last_cloud_sync_at: string | null }
  observed_at: string
}
export interface HistoryPoint { t: string; snapshots: number; time_quality: string; readings: Record<string, { value: number | boolean | null; quality: Quality; unit?: string }> }
export interface History { points: HistoryPoint[]; gaps: { from: string; to: string }[]; resolution: string; truncated: boolean; contains_simulated_data: boolean; from: string; to: string }
export interface StorageInfo {
  status: string; database_bytes: number; wal_bytes: number; disk_total_bytes: number; disk_free_bytes: number
  retention_days: number; telemetry_rows: number; durable_receipts_enabled: boolean
  cloud_sync: { enabled: boolean; outbox_pending: number }
}
