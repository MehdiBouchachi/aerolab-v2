package mqttcontract_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mc "github.com/mehdibouchachi/aerolab-v2/contracts/mqtt"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

var schemaFile = map[string]string{
	"telemetry": "telemetry", "event": "event", "state": "state", "presence": "presence",
	"command": "command", "command_result": "command-result", "receipt": "receipt",
}

func compile(t *testing.T) map[string]*jsonschema.Schema {
	t.Helper()
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	load := func(f string) {
		b, err := os.ReadFile(filepath.Join("schemas", f))
		if err != nil {
			t.Fatal(err)
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		if err := c.AddResource("https://aerolab.invalid/schemas/mqtt/v1/"+f, doc); err != nil {
			t.Fatal(err)
		}
	}
	load("common.schema.json")
	out := map[string]*jsonschema.Schema{}
	for kind, f := range schemaFile {
		load(f + ".schema.json")
		s, err := c.Compile("https://aerolab.invalid/schemas/mqtt/v1/" + f + ".schema.json")
		if err != nil {
			t.Fatalf("compile %s: %v", f, err)
		}
		out[kind] = s
	}
	return out
}

func kindOf(file string) string { return strings.SplitN(filepath.Base(file), ".", 2)[0] }

// Every fixture must get the SAME verdict from the JSON Schema (used by
// firmware / TypeScript) and from the Go validator (used by Pi + simulator).
func TestFixturesAgreeBetweenSchemaAndGo(t *testing.T) {
	schemas := compile(t)
	for _, dir := range []string{"valid", "invalid"} {
		wantValid := dir == "valid"
		files, _ := filepath.Glob(filepath.Join("..", "..", "contracts", "examples", dir, "*.json"))
		if len(files) == 0 {
			files, _ = filepath.Glob(filepath.Join("..", "examples", dir, "*.json"))
		}
		if len(files) == 0 {
			t.Fatalf("no fixtures found for %s", dir)
		}
		for _, f := range files {
			f := f
			t.Run(dir+"/"+filepath.Base(f), func(t *testing.T) {
				raw, err := os.ReadFile(f)
				if err != nil {
					t.Fatal(err)
				}
				kind := kindOf(f)
				sch, ok := schemas[kind]
				if !ok {
					t.Fatalf("no schema for kind %q", kind)
				}
				inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
				if err != nil {
					t.Fatal(err)
				}
				schemaErr := sch.Validate(inst)
				goErr := mc.ValidatePayload(kind, raw)
				if wantValid {
					if schemaErr != nil {
						t.Errorf("JSON Schema rejected valid fixture: %v", schemaErr)
					}
					if goErr != nil {
						t.Errorf("Go validator rejected valid fixture: %v", goErr)
					}
				} else {
					if schemaErr == nil {
						t.Errorf("JSON Schema accepted invalid fixture")
					}
					if goErr == nil {
						t.Errorf("Go validator accepted invalid fixture")
					}
				}
			})
		}
	}
}

// Cross-field rules JSON Schema cannot express (documented in the MQTT
// contract doc) are enforced by the Go validator only.
func TestSemanticRulesGoOnly(t *testing.T) {
	files, _ := filepath.Glob("../examples/invalid-semantic/*.json")
	if len(files) < 2 {
		t.Fatal("semantic fixtures missing")
	}
	for _, f := range files {
		raw, _ := os.ReadFile(f)
		if err := mc.ValidatePayload(kindOf(f), raw); err == nil {
			t.Errorf("%s accepted", f)
		}
	}
}

func TestEveryKindHasFixtures(t *testing.T) {
	for _, k := range mc.Kinds {
		m, _ := filepath.Glob("../examples/valid/" + k + ".*.json")
		if len(m) == 0 {
			t.Errorf("kind %s has no valid fixture", k)
		}
	}
}

func TestOversizeAndDeepPayloadsRejected(t *testing.T) {
	big := `{"x":"` + strings.Repeat("a", mc.MaxPayloadBytes) + `"}`
	if err := mc.ValidatePayload("state", []byte(big)); err == nil || !strings.Contains(err.Error(), "PAYLOAD_TOO_LARGE") {
		t.Fatalf("oversize not rejected: %v", err)
	}
	deep := strings.Repeat(`{"a":`, 20) + `1` + strings.Repeat(`}`, 20)
	if err := mc.ValidatePayload("state", []byte(deep)); err == nil || !strings.Contains(err.Error(), "PAYLOAD_TOO_DEEP") {
		t.Fatalf("deep json not rejected: %v", err)
	}
	if err := mc.ValidatePayload("state", []byte(`{} {}`)); err == nil {
		t.Fatal("trailing data accepted")
	}
}

func TestTopics(t *testing.T) {
	tp := mc.DeviceTopic("esp32-sim-004", mc.TopicTelemetry)
	if tp != "aerolab/v1/devices/esp32-sim-004/telemetry" {
		t.Fatal(tp)
	}
	id, leaf, err := mc.ParseDeviceTopic(tp)
	if err != nil || id != "esp32-sim-004" || leaf != "telemetry" {
		t.Fatal(id, leaf, err)
	}
	for _, bad := range []string{"aerolab/v1/devices/+/state", "aerolab/v1/devices/#", "aerolab/v1/devices/a/b/c", "other/x/y", "aerolab/v1/devices/UPPER/state"} {
		if _, _, err := mc.ParseDeviceTopic(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestProgramBounds(t *testing.T) {
	ok := mc.Program{Mode: "TIMED_CYCLE", OnDurationSeconds: 5, OffDurationSeconds: 60, MaxContinuousOnSecs: 10, RequireReservoirOK: true}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(p *mc.Program){
		"INTERLOCK_CANNOT_BE_DISABLED": func(p *mc.Program) { p.RequireReservoirOK = false },
		"INVALID_ON_DURATION":          func(p *mc.Program) { p.OnDurationSeconds = 0 },
		"INVALID_OFF_DURATION":         func(p *mc.Program) { p.OffDurationSeconds = 100000 },
		"INVALID_MAX_CONTINUOUS":       func(p *mc.Program) { p.MaxContinuousOnSecs = 1 },
		"INVALID_PROGRAM_MODE":         func(p *mc.Program) { p.Mode = "ALWAYS_ON" },
	}
	for code, mut := range cases {
		p := ok
		mut(&p)
		err := p.Validate()
		ve, isV := err.(*mc.ValidationError)
		if !isV || ve.Code != code {
			t.Errorf("%s: got %v", code, err)
		}
	}
}

var _ = json.Marshal
