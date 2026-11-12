// Package synccontract is the wire contract between the Pi edge service and
// the cloud API (outbound HTTPS only; the cloud never connects to the Pi).
//
//	edge -> cloud  POST /api/v1/edge/sync/batch            Batch -> AckResponse
//	edge -> cloud  GET  /api/v1/edge/sync/commands          -> IntentList
//	edge -> cloud  POST /api/v1/edge/sync/command-results   IntentResult -> 204
//
// Identity: the site is derived from the per-site bearer token on the cloud
// side. Batch.SiteID is only a cross-check and is never trusted on its own.
package synccontract

import (
	"encoding/json"
	"fmt"
	"time"

	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
)

const SchemaVersion = 1

// Event types carried in Batch.Events.
const (
	EventTelemetry     = "telemetry.snapshot" // payload: mqttcontract.Telemetry (+ edge fields below)
	EventDeviceEvent   = "device.event"       // payload: mqttcontract.Event
	EventIncident      = "incident.update"    // payload: IncidentUpdate
	EventCommandResult = "command.result"     // payload: map (informational)
)

// Priorities (spec 13.2): P0 current truth / critical, P1 transitions,
// P2 crop & metadata, P3 historical telemetry.
const (
	P0 = 0
	P1 = 1
	P2 = 2
	P3 = 3
)

const (
	MaxBatchEvents = 100
	MaxBatchBytes  = 512 << 10
)

type Batch struct {
	SchemaVersion int     `json:"schema_version"`
	BatchID       string  `json:"batch_id"`
	SiteID        string  `json:"site_id"`
	SentAt        string  `json:"sent_at"`
	EdgeVersion   string  `json:"edge_version"`
	State         *State  `json:"state,omitempty"` // P0 current truth, replaces the previous snapshot
	Events        []Event `json:"events"`
}

type Event struct {
	EventID   string          `json:"event_id"` // stable: the idempotency key
	EventType string          `json:"event_type"`
	Priority  int             `json:"priority"`
	CreatedAt string          `json:"created_at"` // when the edge recorded it (not cloud ingest time)
	Payload   json.RawMessage `json:"payload"`
}

// State is the edge's own view right now. Units are the edge's unit views,
// stored verbatim so the cloud serves the SAME shapes as the Pi (one web app).
type State struct {
	ObservedAt   string            `json:"observed_at"`
	SiteName     string            `json:"site_name"`
	Connectivity Connectivity      `json:"connectivity"`
	Units        []json.RawMessage `json:"units"`
}

type Connectivity struct {
	MQTTBrokerConnected bool    `json:"mqtt_broker_connected"`
	LastCloudSyncAt     *string `json:"last_cloud_sync_at"`
	SyncBacklog         int     `json:"sync_backlog"`
}

// RecordEnvelope wraps a device record with the edge's placement of it in time.
// Original device fields stay untouched inside Record; EffectiveAtUTC +
// TimeQuality say how the Pi placed it (device clock / estimated / receive time).
type RecordEnvelope struct {
	Record         json.RawMessage `json:"record"` // mqttcontract.Telemetry or mqttcontract.Event
	EffectiveAtUTC string          `json:"effective_at_utc"`
	TimeQuality    string          `json:"time_quality"`
	RecordedAt     string          `json:"recorded_at"` // when the Pi durably stored it
}

// IncidentUpdate carries a full incident snapshot plus the edge time of the
// change. The cloud keeps the update with the greatest ChangedAt, so a late
// upload of an old transition can never re-open a resolved incident.
type IncidentUpdate struct {
	ID              string  `json:"id"`
	UnitID          string  `json:"unit_id"`
	Severity        string  `json:"severity"`
	Code            string  `json:"code"`
	Status          string  `json:"status"`
	Title           string  `json:"title"`
	Cause           string  `json:"cause"`
	SuggestedAction string  `json:"suggested_action"`
	FirstSeenAt     string  `json:"first_seen_at"`
	LastSeenAt      string  `json:"last_seen_at"`
	Occurrences     int     `json:"occurrences"`
	AcknowledgedBy  *string `json:"acknowledged_by"`
	AcknowledgedAt  *string `json:"acknowledged_at"`
	ResolvedAt      *string `json:"resolved_at"`
	ChangedAt       string  `json:"changed_at"`
}

type AckResponse struct {
	Acked      []string          `json:"acked"`    // every event_id now durable in the cloud (new or duplicate)
	Rejected   map[string]string `json:"rejected"` // event_id -> reason (poisoned items; edge must stop resending them)
	Duplicates int               `json:"duplicates"`
	ServerTime string            `json:"server_time"`
}

// Intent is a remote programme request the cloud authorised. It is a request,
// never an actuation: the Pi revalidates and the ESP32 confirms.
type Intent struct {
	ID                     string     `json:"id"`
	UnitID                 string     `json:"unit_id"`
	ExpectedProgramVersion int        `json:"expected_program_version"`
	Program                mc.Program `json:"program"`
	RequestedBy            string     `json:"requested_by"`
	CreatedAt              string     `json:"created_at"`
	ExpiresAt              string     `json:"expires_at"`
}

type IntentList struct {
	Intents    []Intent `json:"intents"`
	ServerTime string   `json:"server_time"`
}

// Intent statuses as shown to remote users. APPLIED is only ever set from an
// edge report that itself came from an ESP32 confirmation.
const (
	IntentPendingSite   = "PENDING_SITE"   // stored in the cloud, not yet picked up by the Pi
	IntentPendingDevice = "PENDING_DEVICE" // the Pi accepted it and is waiting for the controller
	IntentApplied       = "APPLIED"
	IntentRejected      = "REJECTED"
	IntentConflict      = "CONFLICT"
	IntentExpired       = "EXPIRED"
	IntentUnknown       = "UNKNOWN"
)

type IntentResult struct {
	CommandID      string `json:"command_id"`
	Status         string `json:"status"`
	Reason         string `json:"reason,omitempty"`
	AppliedVersion *int   `json:"applied_version,omitempty"`
	ObservedAt     string `json:"observed_at"`
}

// Validate checks a batch's structure and bounds (cloud-side input guard).
func (b *Batch) Validate() error {
	if b.SchemaVersion != SchemaVersion {
		return fmt.Errorf("UNSUPPORTED_SCHEMA_VERSION: got %d want %d", b.SchemaVersion, SchemaVersion)
	}
	if !mc.ValidID(b.SiteID) {
		return fmt.Errorf("INVALID_SITE_ID")
	}
	if len(b.BatchID) < 8 || len(b.BatchID) > 64 {
		return fmt.Errorf("INVALID_BATCH_ID")
	}
	if len(b.Events) > MaxBatchEvents {
		return fmt.Errorf("TOO_MANY_EVENTS: %d > %d", len(b.Events), MaxBatchEvents)
	}
	if _, err := time.Parse(time.RFC3339, b.SentAt); err != nil {
		return fmt.Errorf("INVALID_SENT_AT")
	}
	return nil
}

func (r *IntentResult) Validate() error {
	switch r.Status {
	case IntentPendingDevice, IntentApplied, IntentRejected, IntentConflict, IntentExpired, IntentUnknown:
	default:
		return fmt.Errorf("INVALID_STATUS")
	}
	if len(r.CommandID) < 8 || len(r.CommandID) > 64 {
		return fmt.Errorf("INVALID_COMMAND_ID")
	}
	return nil
}
