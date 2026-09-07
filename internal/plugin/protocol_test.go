package plugin

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestCapabilitiesCommandsDeclared(t *testing.T) {
	caps := Capabilities{Commands: true, Version: ProtocolVersion}
	if !caps.HasAny() {
		t.Error("Commands-only capabilities should count as having a capability")
	}
	if err := caps.Validate(); err != nil {
		t.Errorf("Commands-only capabilities should validate: %v", err)
	}
	if caps.Mask()&PresenceCommands == 0 {
		t.Error("Commands capability should set the PresenceCommands bit")
	}
}

func TestCapabilitiesCommandsRoundTrip(t *testing.T) {
	caps := Capabilities{Commands: true}
	data, err := json.Marshal(caps)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"commands":true`) {
		t.Errorf("marshalled capabilities should carry commands=true, got %s", data)
	}
	var out Capabilities
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !out.Commands {
		t.Error("unmarshalled capabilities lost the commands flag")
	}
}

func TestCommandDefRoundTrip(t *testing.T) {
	in := CommandDef{Name: "auth", Description: "manage credentials", Usage: "auth [login|status]"}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(data)
	for _, want := range []string{`"name":"auth"`, `"description":"manage credentials"`, `"usage":"auth [login|status]"`} {
		if !strings.Contains(body, want) {
			t.Errorf("marshalled command missing %s, got %s", want, body)
		}
	}
	var out CommandDef
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("round-trip mismatch:\n in=%+v\nout=%+v", in, out)
	}
}

func TestCommandDefUsageOmitEmpty(t *testing.T) {
	data, err := json.Marshal(CommandDef{Name: "auth", Description: "d"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), `"usage"`) {
		t.Errorf("omitted usage should not be marshalled, got %s", data)
	}
}

func TestCommandsListResultRoundTrip(t *testing.T) {
	in := CommandsListResult{Commands: []CommandDef{{Name: "auth", Description: "d"}}}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"commands":[`) {
		t.Errorf("expected commands array, got %s", data)
	}
	var out CommandsListResult
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Commands) != 1 || out.Commands[0].Name != "auth" {
		t.Errorf("round-trip mismatch: %+v", out)
	}
}

func TestCommandsRunParamsRoundTrip(t *testing.T) {
	in := CommandsRunParams{Name: "auth", Arguments: "login --host tenant.ghe.com"}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, `"arguments":"login --host tenant.ghe.com"`) {
		t.Errorf("arguments should carry the raw string, got %s", body)
	}
	var out CommandsRunParams
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Name != "auth" || out.Arguments != in.Arguments {
		t.Errorf("round-trip mismatch: %+v", out)
	}
}

func TestCommandsRunResultTextRoundTrip(t *testing.T) {
	in := CommandsRunResult{Text: "Open the URL and enter code ABCD-1234"}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, `"text":"Open`) {
		t.Errorf("expected text field, got %s", body)
	}
	if strings.Contains(body, `"data"`) {
		t.Errorf("omitted data should not be marshalled, got %s", body)
	}
	var out CommandsRunResult
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Text != in.Text || out.Data != nil {
		t.Errorf("round-trip mismatch: %+v", out)
	}
}

func TestCommandsRunResultDataRoundTrip(t *testing.T) {
	in := CommandsRunResult{Data: map[string]any{"host": "tenant.ghe.com", "user": "dan"}}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, `"data":`) {
		t.Errorf("data should be marshalled when text is empty, got %s", body)
	}
	if strings.Contains(body, `"text"`) {
		t.Errorf("omitted text should not be marshalled, got %s", body)
	}
	var out CommandsRunResult
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got := out.Data.(map[string]any)
	if got["host"] != "tenant.ghe.com" {
		t.Errorf("round-trip data mismatch: %+v", out.Data)
	}
}

func TestCommandsHelpParamsOmitEmptyName(t *testing.T) {
	data, err := json.Marshal(CommandsHelpParams{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := m["name"]; present {
		t.Errorf("empty optional name should be omitted, got %s", data)
	}
}

func TestCommandsHelpResultRoundTrip(t *testing.T) {
	in := CommandsHelpResult{Text: "auth manages the credential"}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"text":"auth manages the credential"`) {
		t.Errorf("expected help text, got %s", data)
	}
	var out CommandsHelpResult
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Text != in.Text {
		t.Errorf("round-trip mismatch: %+v", out)
	}
}

func TestCommandsMethodConstants(t *testing.T) {
	got := map[string]string{
		MethodCommandsList: MethodCommandsList,
		MethodCommandsRun:  MethodCommandsRun,
		MethodCommandsHelp: MethodCommandsHelp,
	}
	want := map[string]string{
		MethodCommandsList: "commands/list",
		MethodCommandsRun:  "commands/run",
		MethodCommandsHelp: "commands/help",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s should be %q, got %q", k, v, got[k])
		}
	}
}
