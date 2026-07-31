package script

import (
	"bytes"
	"encoding/json"
)

// Toggle is the canonical tri-state for OutputSpec postprocessor
// flags. The precedence chain is:
//
//	ToggleDefault  — caller did not specify; defer to preset/config/safety
//	ToggleEnabled  — caller explicitly enabled this processor
//	ToggleDisabled — caller explicitly disabled this processor
//
// Resolve() algorithm:
//
//	if caller != ToggleDefault: caller
//	elif preset != ToggleDefault: preset
//	elif config != ToggleDefault: config
//	else: safety
type Toggle string

const (
	// ToggleDefault — no preference; downstream layers decide.
	ToggleDefault Toggle = "default"
	// ToggleEnabled — explicitly enabled.
	ToggleEnabled Toggle = "enabled"
	// ToggleDisabled — explicitly disabled.
	ToggleDisabled Toggle = "disabled"
)

// ToggleFromBool converts a legacy bool to the canonical Toggle
// (true → ToggleEnabled, false → ToggleDisabled). Used by callers
// that haven't migrated to explicit Toggle values.
func ToggleFromBool(b bool) Toggle {
	if b {
		return ToggleEnabled
	}
	return ToggleDisabled
}

// Resolve applies the precedence chain to a sequence of Toggles
// (caller, preset, config, safety) and returns the resolved value.
func (t Toggle) Resolve(caller, preset, config, safety Toggle) Toggle {
	if caller != ToggleDefault {
		return caller
	}
	if preset != ToggleDefault {
		return preset
	}
	if config != ToggleDefault {
		return config
	}
	return safety
}

// AsBool collapses the resolved toggle to a boolean. Only ToggleEnabled
// resolves to true. ToggleDisabled + ToggleDefault + the Go zero value
// Toggle("") all resolve to false.
//
// Semantics (godlike/07 NO-FAKE-AVAILABILITY): caller-omitted is
// treated as "no opt-in" (false) at the gate boundary. The legacy
// bool=true intent maps to ToggleEnabled → AsBool=true; the legacy
// bool=false intent maps to ToggleDisabled OR ToggleDefault → either
// of which resolves to false. Pre-PR-3 callers sending {}/caller-omitted
// see identical false semantics at the gate (zero bool = false both
// before and after the Toggle migration).
//
// Forward-pointer: applySafetyDefaults converts ToggleDefault →
// ToggleEnabled in the worker, so the post-safety-default
// HasAnyPostprocessor=AsBool() OR returns true and the registered
// postprocessors run for caller-omitted payloads. The preflight
// gate evaluates pre-safety and therefore does NOT block caller-omitted
// (mismatch with required-service is forward-pointer
// PR-PREFLIGHT-RUN-AFTER-SAFETY).
func (t Toggle) AsBool() bool {
	return t == ToggleEnabled
}

// UnmarshalJSON is dispatched by Go's json package ONLY when the
// method name is exactly "UnmarshalJSON" — this is the canonical
// json.Unmarshaler interface (golang.org/pkg/encoding/json). Accepts
// the canonical Toggle string form (3 valid values per the const
// block) AND the legacy bool form (true → ToggleEnabled,
// false → ToggleDisabled), plus JSON null → ToggleDefault (caller
// explicit nulls []resolve to "no preference", the same as omitting
// the field). Returns an error if the input is neither string, bool,
// nor null, OR if the string value is not one of the 3 canonical
// tokens.
//
// godlike/06 SSOT: this wire-shape adapter lives on the domain type
// itself (single canonical owner) so HTTP DTO layers and outbound
// marshalers do NOT need duplicate compat shims.
func (t *Toggle) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*t = ToggleDefault
		return nil
	}
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		switch Toggle(asString) {
		case ToggleDefault, ToggleEnabled, ToggleDisabled:
			*t = Toggle(asString)
			return nil
		}
		return &toggleInvalidStringError{raw: asString}
	}
	var asBool bool
	if err := json.Unmarshal(data, &asBool); err == nil {
		*t = ToggleFromBool(asBool)
		return nil
	}
	return &toggleInvalidTypeError{data: data}
}

// MarshalJSON (canonical json.Marshaler interface name — Go's json
// package dispatches only by exact name) emits the canonical Toggle
// string form. ToggleDefault MARSHALS AS THE STRING "default" —
// outbound wire-shape includes the explicit "default" token for
// fields that were unset (no override applied). For inbound compat,
// JSON `"key": "default"` strings also round-trip correctly.
func (t Toggle) MarshalJSON() ([]byte, error) {
	switch t {
	case ToggleDefault, ToggleEnabled, ToggleDisabled:
		return json.Marshal(string(t))
	}
	return nil, &toggleInvalidStringError{raw: string(t)}
}

// toggleInvalidStringError signals a non-canonical Toggle string
// during wire (un)marshaling. Reachable only via unmarshaling invalid
// data (operator-facing API fails closed) or via a buggy migration
// (compile-time string Set ensures the const block is exhaustive).
type toggleInvalidStringError struct {
	raw string
}

func (e *toggleInvalidStringError) Error() string {
	return "script: invalid Toggle string value: " + e.raw +
		" (canonical: \"default\", \"enabled\", \"disabled\")"
}

// toggleInvalidTypeError signals a wire payload that is neither string
// nor bool during SafeUnmarshalJSON.
type toggleInvalidTypeError struct {
	data []byte
}

func (e *toggleInvalidTypeError) Error() string {
	return "script: Toggle wire payload must be string or bool; got: " +
		string(bytes.TrimSpace(e.data))
}
