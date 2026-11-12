package mqttcontract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"time"
)

// DataSource distinguishes real hardware from simulator output. Simulator
// data must never be presentable as real measurements.
type DataSource string

const (
	SourceDevice    DataSource = "DEVICE"
	SourceSimulator DataSource = "SIMULATOR"
)

type ClockQuality string

const (
	ClockMonotonicOnly ClockQuality = "monotonic_only"
	ClockRTCSynced     ClockQuality = "rtc_synced"
	ClockNTPSynced     ClockQuality = "ntp_synced"
)

type ReadingQuality string

const (
	QualityValid    ReadingQuality = "valid"
	QualityInvalid  ReadingQuality = "invalid"
	QualityMissing  ReadingQuality = "missing"
	QualityOutdated ReadingQuality = "outdated"
)

type ControlState string

const (
	StateBootSafe      ControlState = "BOOT_SAFE"
	StateSelfCheck     ControlState = "SELF_CHECK"
	StateNeedsSetup    ControlState = "NEEDS_SETUP"
	StateRunning       ControlState = "RUNNING"
	StateDegraded      ControlState = "DEGRADED"
	StateProtectedStop ControlState = "PROTECTED_STOP"
)

// Fault codes the contract knows about. Unknown codes are accepted (forward
// compatibility) but only these have curated incident text on the Pi.
const (
	FaultLowWater       = "LOW_WATER"
	FaultLevelSensor    = "LEVEL_SENSOR_INVALID"
	FaultPHInvalid      = "PH_INVALID"
	FaultECInvalid      = "EC_INVALID"
	FaultNoProgram      = "NO_VALID_PROGRAM"
	FaultStorageDegrade = "BUFFER_OVERFLOW"
)

// Reading is one sensor value. Value is nil when quality is missing/invalid;
// a missing reading is never encoded as zero. Value may be a number or bool.
type Reading struct {
	Value   any            `json:"value"`
	Quality ReadingQuality `json:"quality"`
	Unit    string         `json:"unit,omitempty"`
}

// Header is the identity block shared by every device-originated message.
type Header struct {
	SchemaVersion int        `json:"schema_version"`
	SiteID        string     `json:"site_id"`
	DeviceID      string     `json:"device_id"`
	UnitID        *string    `json:"unit_id"`
	BootID        string     `json:"boot_id"`
	DataSource    DataSource `json:"data_source"`
}

// Telemetry is a durable, sequenced sensor snapshot (also used for replay).
type Telemetry struct {
	Header
	Type                  string             `json:"type"`
	Seq                   int64              `json:"seq"`
	UptimeMS              int64              `json:"uptime_ms"`
	SampledAtUTC          *string            `json:"sampled_at_utc"`
	ClockQuality          ClockQuality       `json:"clock_quality"`
	ProgramVersionApplied *int               `json:"program_version_applied"`
	Readings              map[string]Reading `json:"readings"`
}

// EventType enumerates durable device events.
type EventType string

const (
	EventFaultRaised     EventType = "FAULT_RAISED"
	EventFaultCleared    EventType = "FAULT_CLEARED"
	EventStateChange     EventType = "STATE_CHANGE"
	EventProgramApplied  EventType = "PROGRAM_APPLIED"
	EventBoot            EventType = "BOOT"
	EventBufferOverflow  EventType = "BUFFER_OVERFLOW"
	EventRecoveryConfirm EventType = "RECOVERY_CONFIRMED"
)

type Severity string

const (
	SevInfo     Severity = "info"
	SevWarning  Severity = "warning"
	SevCritical Severity = "critical"
)

// Event is a durable, sequenced device event. Seq shares one counter with
// Telemetry inside a boot_id.
type Event struct {
	Header
	Type         string         `json:"type"`
	Seq          int64          `json:"seq"`
	UptimeMS     int64          `json:"uptime_ms"`
	SampledAtUTC *string        `json:"sampled_at_utc"`
	ClockQuality ClockQuality   `json:"clock_quality"`
	EventType    EventType      `json:"event_type"`
	Severity     Severity       `json:"severity"`
	Code         string         `json:"code"`
	Details      map[string]any `json:"details,omitempty"`
}

// BufferStatus describes the device's bounded flash backlog.
type BufferStatus struct {
	Occupancy   int   `json:"occupancy"`
	Capacity    int   `json:"capacity"`
	Unreceipted int   `json:"unreceipted"`
	Overflowed  int64 `json:"overflow_count"`
}

// State is the 5-second heartbeat. It is ephemeral: the Pi keeps the latest
// in memory and persists only transitions.
type State struct {
	Header
	UptimeMS              int64        `json:"uptime_ms"`
	ControlState          ControlState `json:"control_state"`
	PumpCommanded         string       `json:"pump_commanded"`
	WaterDelivery         string       `json:"water_delivery"`
	ProgramVersionApplied *int         `json:"program_version_applied"`
	LastCommandID         *string      `json:"last_command_id"`
	EssentialInterlocksOK bool         `json:"essential_interlocks_ok"`
	FaultCodes            []string     `json:"fault_codes"`
	FreeHeapBytes         int64        `json:"free_heap_bytes"`
	WifiRSSIDBM           int          `json:"wifi_rssi_dbm"`
	FirmwareVersion       string       `json:"firmware_version"`
	SessionNonce          string       `json:"session_nonce"`
	Buffer                BufferStatus `json:"buffer"`
}

type PresenceStatus string

const (
	PresenceOnline  PresenceStatus = "ONLINE"
	PresenceOffline PresenceStatus = "OFFLINE"
)

// Presence is the birth / last-will message.
type Presence struct {
	Header
	Status          PresenceStatus `json:"status"`
	FirmwareVersion string         `json:"firmware_version"`
	SessionNonce    string         `json:"session_nonce"`
}

type CommandType string

const (
	CmdApplyProgram CommandType = "APPLY_PROGRAM"
	CmdQueryState   CommandType = "QUERY_STATE"
)

// Program is a complete irrigation programme. These are generic bounded
// timing fields; this repository ships NO agronomically approved values.
type Program struct {
	Mode                string `json:"mode"`
	OnDurationSeconds   int    `json:"on_duration_seconds"`
	OffDurationSeconds  int    `json:"off_duration_seconds"`
	MaxContinuousOnSecs int    `json:"max_continuous_on_seconds"`
	RequireReservoirOK  bool   `json:"require_reservoir_ok"`
}

// Command is Pi -> device. It is never retained. SessionNonce binds it to the
// device's current MQTT session so a command parked in the broker across a
// reconnect cannot execute later.
type Command struct {
	SchemaVersion          int         `json:"schema_version"`
	CommandID              string      `json:"command_id"`
	SiteID                 string      `json:"site_id"`
	DeviceID               string      `json:"device_id"`
	UnitID                 string      `json:"unit_id"`
	CommandType            CommandType `json:"command_type"`
	SessionNonce           string      `json:"session_nonce"`
	ExpectedProgramVersion int         `json:"expected_program_version"`
	NewProgramVersion      int         `json:"new_program_version,omitempty"`
	IssuedAtUTC            string      `json:"issued_at_utc"`
	ExpiresAtUTC           string      `json:"expires_at_utc"`
	TTLSeconds             int         `json:"ttl_seconds"`
	Program                *Program    `json:"program,omitempty"`
}

type ResultStatus string

const (
	ResultApplied          ResultStatus = "APPLIED"
	ResultRejected         ResultStatus = "REJECTED"
	ResultAlreadyProcessed ResultStatus = "ALREADY_PROCESSED"
	ResultUnknown          ResultStatus = "UNKNOWN"
)

// CommandResult is device -> Pi.
type CommandResult struct {
	SchemaVersion         int          `json:"schema_version"`
	CommandID             string       `json:"command_id"`
	SiteID                string       `json:"site_id"`
	DeviceID              string       `json:"device_id"`
	BootID                string       `json:"boot_id"`
	DataSource            DataSource   `json:"data_source"`
	Status                ResultStatus `json:"status"`
	OriginalStatus        ResultStatus `json:"original_status,omitempty"`
	CurrentProgramVersion *int         `json:"current_program_version"`
	ReasonCode            *string      `json:"reason_code"`
}

// Receipt is Pi -> device and is the ONLY signal that lets a device reclaim
// buffered records. It is emitted after the SQLite transaction committed.
type Receipt struct {
	SchemaVersion     int     `json:"schema_version"`
	SiteID            string  `json:"site_id"`
	DeviceID          string  `json:"device_id"`
	BootID            string  `json:"boot_id"`
	ReceiptID         string  `json:"receipt_id"`
	Status            string  `json:"status"`
	AckedSequences    []int64 `json:"acked_sequences"`
	RejectedSequences []int64 `json:"rejected_sequences"`
}

// ----------------------------------------------------------------- decoding

// DecodeStrict parses JSON into v, rejects trailing data and oversize
// payloads. Unknown fields are tolerated (additive evolution inside v1).
func DecodeStrict(data []byte, v any) error {
	if len(data) == 0 {
		return &ValidationError{Code: "EMPTY_PAYLOAD", Field: "", Msg: "empty payload"}
	}
	if len(data) > MaxPayloadBytes {
		return &ValidationError{Code: "PAYLOAD_TOO_LARGE", Msg: fmt.Sprintf("payload %d bytes exceeds %d", len(data), MaxPayloadBytes)}
	}
	if jsonDepth(data) > 8 {
		return &ValidationError{Code: "PAYLOAD_TOO_DEEP", Msg: "json nesting exceeds 8"}
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(v); err != nil {
		return &ValidationError{Code: "MALFORMED_JSON", Msg: err.Error()}
	}
	if dec.More() {
		return &ValidationError{Code: "MALFORMED_JSON", Msg: "trailing data"}
	}
	return nil
}

func jsonDepth(b []byte) int {
	depth, max := 0, 0
	inStr, esc := false, false
	for _, c := range b {
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{', '[':
			depth++
			if depth > max {
				max = depth
			}
		case '}', ']':
			depth--
		}
	}
	return max
}

// ValidationError carries a stable machine code (for counters and API error
// details) plus the offending field.
type ValidationError struct {
	Code  string
	Field string
	Msg   string
}

func (e *ValidationError) Error() string {
	if e.Field != "" {
		return e.Code + ": " + e.Field + ": " + e.Msg
	}
	return e.Code + ": " + e.Msg
}

func verr(code, field, msg string) error { return &ValidationError{Code: code, Field: field, Msg: msg} }

// ---------------------------------------------------------------- validation

var (
	bootIDPattern  = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)
	cmdIDPattern   = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)
	readingNameRe  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)
	faultCodeRe    = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,39}$`)
	nonceRe        = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)
	versionPattern = regexp.MustCompile(`^[A-Za-z0-9._+-]{1,32}$`)
)

const (
	maxReadings   = 32
	maxFaultCodes = 16
	maxDetails    = 16
	maxSeq        = int64(1) << 52
	maxAcks       = 100
)

func (h *Header) validate() error {
	if h.SchemaVersion != SchemaVersion {
		return verr("UNSUPPORTED_SCHEMA_VERSION", "schema_version", fmt.Sprintf("got %d, supported %d", h.SchemaVersion, SchemaVersion))
	}
	if !ValidID(h.SiteID) {
		return verr("INVALID_SITE_ID", "site_id", "illegal id")
	}
	if !ValidID(h.DeviceID) {
		return verr("INVALID_DEVICE_ID", "device_id", "illegal id")
	}
	if h.UnitID != nil && !ValidID(*h.UnitID) {
		return verr("INVALID_UNIT_ID", "unit_id", "illegal id")
	}
	if !bootIDPattern.MatchString(h.BootID) {
		return verr("INVALID_BOOT_ID", "boot_id", "illegal boot id")
	}
	if h.DataSource != SourceDevice && h.DataSource != SourceSimulator {
		return verr("INVALID_DATA_SOURCE", "data_source", "must be DEVICE or SIMULATOR")
	}
	return nil
}

func validClock(c ClockQuality) bool {
	return c == ClockMonotonicOnly || c == ClockRTCSynced || c == ClockNTPSynced
}

func validTime(p *string) bool {
	if p == nil {
		return true
	}
	_, err := time.Parse(time.RFC3339, *p)
	return err == nil
}

// A device that claims an absolute clock must supply a timestamp, and one
// that only has a monotonic clock must NOT invent one.
func clockConsistent(q ClockQuality, sampled *string) error {
	if !validClock(q) {
		return verr("INVALID_CLOCK_QUALITY", "clock_quality", "unknown value")
	}
	if !validTime(sampled) {
		return verr("INVALID_TIMESTAMP", "sampled_at_utc", "not RFC3339")
	}
	if q == ClockMonotonicOnly && sampled != nil {
		return verr("FABRICATED_TIMESTAMP", "sampled_at_utc", "monotonic_only clock must not carry an absolute timestamp")
	}
	if q != ClockMonotonicOnly && sampled == nil {
		return verr("MISSING_TIMESTAMP", "sampled_at_utc", "clock quality claims absolute time but none supplied")
	}
	return nil
}

func (t *Telemetry) Validate() error {
	if err := t.Header.validate(); err != nil {
		return err
	}
	if t.Type != "telemetry" {
		return verr("INVALID_TYPE", "type", "expected telemetry")
	}
	if t.Seq < 1 || t.Seq > maxSeq {
		return verr("INVALID_SEQ", "seq", "out of range")
	}
	if t.UptimeMS < 0 {
		return verr("INVALID_UPTIME", "uptime_ms", "negative")
	}
	if err := clockConsistent(t.ClockQuality, t.SampledAtUTC); err != nil {
		return err
	}
	if t.ProgramVersionApplied != nil && *t.ProgramVersionApplied < 0 {
		return verr("INVALID_PROGRAM_VERSION", "program_version_applied", "negative")
	}
	if len(t.Readings) == 0 || len(t.Readings) > maxReadings {
		return verr("INVALID_READINGS", "readings", "1..32 readings required")
	}
	for name, r := range t.Readings {
		if !readingNameRe.MatchString(name) {
			return verr("INVALID_READING_NAME", "readings."+name, "illegal sensor name")
		}
		switch r.Quality {
		case QualityValid:
			switch v := r.Value.(type) {
			case bool:
			case float64:
				if v != v || v > 1e9 || v < -1e9 { // NaN / absurd
					return verr("INVALID_READING_VALUE", "readings."+name, "out of range")
				}
			default:
				return verr("INVALID_READING_VALUE", "readings."+name, "valid reading needs numeric or boolean value")
			}
		case QualityInvalid, QualityMissing, QualityOutdated:
			// A value may accompany invalid/outdated for diagnostics, but a
			// missing reading must never carry a (zero) value.
			if r.Quality == QualityMissing && r.Value != nil {
				return verr("INVALID_READING_VALUE", "readings."+name, "missing reading must have null value")
			}
		default:
			return verr("INVALID_READING_QUALITY", "readings."+name, "unknown quality")
		}
	}
	return nil
}

func (e *Event) Validate() error {
	if err := e.Header.validate(); err != nil {
		return err
	}
	if e.Type != "event" {
		return verr("INVALID_TYPE", "type", "expected event")
	}
	if e.Seq < 1 || e.Seq > maxSeq {
		return verr("INVALID_SEQ", "seq", "out of range")
	}
	if e.UptimeMS < 0 {
		return verr("INVALID_UPTIME", "uptime_ms", "negative")
	}
	if err := clockConsistent(e.ClockQuality, e.SampledAtUTC); err != nil {
		return err
	}
	switch e.EventType {
	case EventFaultRaised, EventFaultCleared, EventStateChange, EventProgramApplied, EventBoot, EventBufferOverflow, EventRecoveryConfirm:
	default:
		return verr("INVALID_EVENT_TYPE", "event_type", "unknown")
	}
	switch e.Severity {
	case SevInfo, SevWarning, SevCritical:
	default:
		return verr("INVALID_SEVERITY", "severity", "unknown")
	}
	if !faultCodeRe.MatchString(e.Code) {
		return verr("INVALID_CODE", "code", "illegal code")
	}
	if len(e.Details) > maxDetails {
		return verr("INVALID_DETAILS", "details", "too many keys")
	}
	return nil
}

func (s *State) Validate() error {
	if err := s.Header.validate(); err != nil {
		return err
	}
	switch s.ControlState {
	case StateBootSafe, StateSelfCheck, StateNeedsSetup, StateRunning, StateDegraded, StateProtectedStop:
	default:
		return verr("INVALID_CONTROL_STATE", "control_state", "unknown")
	}
	if s.PumpCommanded != "ON" && s.PumpCommanded != "OFF" {
		return verr("INVALID_PUMP_STATE", "pump_commanded", "must be ON or OFF")
	}
	switch s.WaterDelivery {
	case "NOT_VERIFIED", "CONFIRMED", "NOT_FLOWING":
	default:
		return verr("INVALID_WATER_DELIVERY", "water_delivery", "unknown")
	}
	if s.UptimeMS < 0 {
		return verr("INVALID_UPTIME", "uptime_ms", "negative")
	}
	if s.WifiRSSIDBM < -127 || s.WifiRSSIDBM > 0 {
		return verr("INVALID_RSSI", "wifi_rssi_dbm", "must be -127..0")
	}
	if s.ProgramVersionApplied != nil && *s.ProgramVersionApplied < 0 {
		return verr("INVALID_PROGRAM_VERSION", "program_version_applied", "negative")
	}
	if s.LastCommandID != nil && !cmdIDPattern.MatchString(*s.LastCommandID) {
		return verr("INVALID_COMMAND_ID", "last_command_id", "illegal")
	}
	if len(s.FaultCodes) > maxFaultCodes {
		return verr("INVALID_FAULT_CODES", "fault_codes", "too many")
	}
	for _, c := range s.FaultCodes {
		if !faultCodeRe.MatchString(c) {
			return verr("INVALID_FAULT_CODES", "fault_codes", "illegal code "+c)
		}
	}
	if !nonceRe.MatchString(s.SessionNonce) {
		return verr("INVALID_SESSION_NONCE", "session_nonce", "illegal")
	}
	if !versionPattern.MatchString(s.FirmwareVersion) {
		return verr("INVALID_FIRMWARE_VERSION", "firmware_version", "illegal")
	}
	if s.Buffer.Capacity < 0 || s.Buffer.Occupancy < 0 || s.Buffer.Unreceipted < 0 {
		return verr("INVALID_BUFFER", "buffer", "negative")
	}
	return nil
}

func (p *Presence) Validate() error {
	if err := p.Header.validate(); err != nil {
		return err
	}
	if p.Status != PresenceOnline && p.Status != PresenceOffline {
		return verr("INVALID_PRESENCE", "status", "must be ONLINE or OFFLINE")
	}
	if !nonceRe.MatchString(p.SessionNonce) {
		return verr("INVALID_SESSION_NONCE", "session_nonce", "illegal")
	}
	return nil
}

func (r *CommandResult) Validate() error {
	if r.SchemaVersion != SchemaVersion {
		return verr("UNSUPPORTED_SCHEMA_VERSION", "schema_version", "unsupported")
	}
	if !cmdIDPattern.MatchString(r.CommandID) {
		return verr("INVALID_COMMAND_ID", "command_id", "illegal")
	}
	if !ValidID(r.SiteID) || !ValidID(r.DeviceID) {
		return verr("INVALID_ID", "site_id/device_id", "illegal")
	}
	if !bootIDPattern.MatchString(r.BootID) {
		return verr("INVALID_BOOT_ID", "boot_id", "illegal")
	}
	if r.DataSource != SourceDevice && r.DataSource != SourceSimulator {
		return verr("INVALID_DATA_SOURCE", "data_source", "must be DEVICE or SIMULATOR")
	}
	switch r.Status {
	case ResultApplied, ResultRejected, ResultAlreadyProcessed, ResultUnknown:
	default:
		return verr("INVALID_RESULT_STATUS", "status", "unknown")
	}
	if r.Status == ResultRejected && (r.ReasonCode == nil || *r.ReasonCode == "") {
		return verr("MISSING_REASON", "reason_code", "rejected results must explain why")
	}
	if r.CurrentProgramVersion != nil && *r.CurrentProgramVersion < 0 {
		return verr("INVALID_PROGRAM_VERSION", "current_program_version", "negative")
	}
	return nil
}

func (r *Receipt) Validate() error {
	if r.SchemaVersion != SchemaVersion {
		return verr("UNSUPPORTED_SCHEMA_VERSION", "schema_version", "unsupported")
	}
	if !ValidID(r.SiteID) || !ValidID(r.DeviceID) || !bootIDPattern.MatchString(r.BootID) {
		return verr("INVALID_ID", "", "illegal identity")
	}
	if r.Status != "PERSISTED" {
		return verr("INVALID_RECEIPT_STATUS", "status", "must be PERSISTED")
	}
	if len(r.AckedSequences) > maxAcks || len(r.RejectedSequences) > maxAcks {
		return verr("TOO_MANY_ACKS", "acked_sequences", "exceeds 100")
	}
	if len(r.AckedSequences)+len(r.RejectedSequences) == 0 {
		return verr("EMPTY_RECEIPT", "acked_sequences", "receipt acknowledges nothing")
	}
	if r.ReceiptID == "" {
		return verr("MISSING_RECEIPT_ID", "receipt_id", "required")
	}
	return nil
}

func (c *Command) Validate() error {
	if c.SchemaVersion != SchemaVersion {
		return verr("UNSUPPORTED_SCHEMA_VERSION", "schema_version", "unsupported")
	}
	if !cmdIDPattern.MatchString(c.CommandID) {
		return verr("INVALID_COMMAND_ID", "command_id", "illegal")
	}
	if !ValidID(c.SiteID) || !ValidID(c.DeviceID) || !ValidID(c.UnitID) {
		return verr("INVALID_ID", "", "illegal identity")
	}
	if !nonceRe.MatchString(c.SessionNonce) {
		return verr("INVALID_SESSION_NONCE", "session_nonce", "illegal")
	}
	if c.TTLSeconds < 1 || c.TTLSeconds > 300 {
		return verr("INVALID_TTL", "ttl_seconds", "1..300 required")
	}
	iss, err1 := time.Parse(time.RFC3339, c.IssuedAtUTC)
	exp, err2 := time.Parse(time.RFC3339, c.ExpiresAtUTC)
	if err1 != nil || err2 != nil {
		return verr("INVALID_TIMESTAMP", "issued_at_utc/expires_at_utc", "not RFC3339")
	}
	if !exp.After(iss) || exp.Sub(iss) > 5*time.Minute {
		return verr("INVALID_EXPIRY", "expires_at_utc", "must be after issued_at and within 5 minutes")
	}
	if c.ExpectedProgramVersion < 0 {
		return verr("INVALID_PROGRAM_VERSION", "expected_program_version", "negative")
	}
	switch c.CommandType {
	case CmdApplyProgram:
		if c.Program == nil {
			return verr("MISSING_PROGRAM", "program", "APPLY_PROGRAM requires a complete program")
		}
		if c.NewProgramVersion != c.ExpectedProgramVersion+1 {
			return verr("INVALID_PROGRAM_VERSION", "new_program_version", "must equal expected_program_version+1")
		}
		return c.Program.Validate()
	case CmdQueryState:
		if c.Program != nil {
			return verr("UNEXPECTED_PROGRAM", "program", "QUERY_STATE carries no program")
		}
		return nil
	default:
		return verr("INVALID_COMMAND_TYPE", "command_type", "unknown (maintenance pump tests are local-only and never travel over MQTT)")
	}
}

// Absolute contract bounds. These are protocol sanity limits, NOT irrigation
// recommendations. Each device additionally enforces its own, usually
// tighter, hardware-profile limit.
const (
	MaxOnSeconds  = 3600
	MaxOffSeconds = 86400
)

// Validate checks that a programme is complete and inside protocol bounds.
// require_reservoir_ok=false is rejected: protection cannot be disabled.
func (p *Program) Validate() error {
	if p.Mode != "TIMED_CYCLE" {
		return verr("INVALID_PROGRAM_MODE", "program.mode", "only TIMED_CYCLE is defined")
	}
	if p.OnDurationSeconds < 1 || p.OnDurationSeconds > MaxOnSeconds {
		return verr("INVALID_ON_DURATION", "program.on_duration_seconds", fmt.Sprintf("1..%d", MaxOnSeconds))
	}
	if p.OffDurationSeconds < 1 || p.OffDurationSeconds > MaxOffSeconds {
		return verr("INVALID_OFF_DURATION", "program.off_duration_seconds", fmt.Sprintf("1..%d", MaxOffSeconds))
	}
	if p.MaxContinuousOnSecs < p.OnDurationSeconds || p.MaxContinuousOnSecs > MaxOnSeconds {
		return verr("INVALID_MAX_CONTINUOUS", "program.max_continuous_on_seconds", "must be >= on_duration_seconds and <= protocol maximum")
	}
	if !p.RequireReservoirOK {
		return verr("INTERLOCK_CANNOT_BE_DISABLED", "program.require_reservoir_ok", "reservoir protection cannot be disabled")
	}
	return nil
}
