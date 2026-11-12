// Package mqttcontract is the single Go implementation of the AeroLab MQTT
// device contract v1 (see docs/architecture/mqtt-contract.md and ./schemas).
//
// Both the Raspberry Pi edge service and the ESP32 simulator import this
// package so they cannot drift apart. The JSON Schemas in ./schemas are the
// language-neutral source of truth for firmware and the TypeScript packages;
// contract_test.go proves the Go validators and the schemas agree on every
// fixture in contracts/examples.
package mqttcontract

import (
	"fmt"
	"regexp"
	"strings"
)

// SchemaVersion is the only wire-format version understood by this package.
// Unknown versions are rejected explicitly, never silently coerced.
const SchemaVersion = 1

// MaxPayloadBytes is the largest MQTT payload any consumer may parse.
const MaxPayloadBytes = 8 * 1024

// Topic leaf names under aerolab/v1/devices/{deviceId}/.
const (
	TopicPresence      = "presence"
	TopicState         = "state"
	TopicTelemetry     = "telemetry"
	TopicEvents        = "events"
	TopicCommands      = "commands"
	TopicCommandResult = "command-results"
	TopicReceipts      = "receipts"
)

const topicPrefix = "aerolab/v1/devices/"

// IDs are used inside topic names and in broker ACL patterns, so they are
// restricted to a conservative, wildcard-free alphabet.
var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,39}$`)

// ValidID reports whether s is a legal device_id / unit_id / site_id token.
func ValidID(s string) bool { return idPattern.MatchString(s) }

// DeviceTopic builds a device-scoped topic.
func DeviceTopic(deviceID, leaf string) string {
	return topicPrefix + deviceID + "/" + leaf
}

// DeviceWildcard builds the Pi-side subscription "aerolab/v1/devices/+/leaf".
func DeviceWildcard(leaf string) string { return topicPrefix + "+/" + leaf }

// ParseDeviceTopic splits "aerolab/v1/devices/{id}/{leaf}".
func ParseDeviceTopic(topic string) (deviceID, leaf string, err error) {
	if !strings.HasPrefix(topic, topicPrefix) {
		return "", "", fmt.Errorf("topic %q outside %s", topic, topicPrefix)
	}
	parts := strings.Split(strings.TrimPrefix(topic, topicPrefix), "/")
	if len(parts) != 2 || !ValidID(parts[0]) || parts[1] == "" {
		return "", "", fmt.Errorf("malformed device topic %q", topic)
	}
	return parts[0], parts[1], nil
}

// DeviceUploadLeaves are the topics a device may publish (ACL write list).
var DeviceUploadLeaves = []string{TopicPresence, TopicState, TopicTelemetry, TopicEvents, TopicCommandResult}

// DeviceDownloadLeaves are the topics a device may subscribe to (ACL read list).
var DeviceDownloadLeaves = []string{TopicCommands, TopicReceipts}
