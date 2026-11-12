package mqttcontract

import "fmt"

// Kinds are the contract message kinds; fixtures are named "<kind>.<case>.json"
// where kind uses '_' in place of '-'.
var Kinds = []string{"telemetry", "event", "state", "presence", "command", "command_result", "receipt"}

// ValidatePayload decodes and validates raw JSON of the given kind using the
// Go implementation. It is used by contract tests (against the JSON Schemas)
// and anywhere a generic check is handy.
func ValidatePayload(kind string, data []byte) error {
	var v interface{ Validate() error }
	switch kind {
	case "telemetry":
		v = &Telemetry{}
	case "event":
		v = &Event{}
	case "state":
		v = &State{}
	case "presence":
		v = &Presence{}
	case "command":
		v = &Command{}
	case "command_result":
		v = &CommandResult{}
	case "receipt":
		v = &Receipt{}
	default:
		return fmt.Errorf("unknown kind %q", kind)
	}
	if err := DecodeStrict(data, v); err != nil {
		return err
	}
	return v.Validate()
}
