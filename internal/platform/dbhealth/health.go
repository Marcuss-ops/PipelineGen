// Package dbhealth defines the shared finding vocabulary for the
// `admin db doctor` gate. It deliberately has no database dependencies so
// both the SQLite and PostgreSQL doctor implementations can return it.
package dbhealth

import "fmt"

type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
)

// Finding is one diagnostic result. Remediation names the ticket or
// migration that owns the repair so the report never suggests deleting data.
type Finding struct {
	Plane       string   `json:"plane"`
	Check       string   `json:"check"`
	Severity    Severity `json:"severity"`
	Detail      string   `json:"detail"`
	Remediation string   `json:"remediation,omitempty"`
}

func (f Finding) String() string {
	s := fmt.Sprintf("[%s] %s %s: %s", f.Plane, f.Severity, f.Check, f.Detail)
	if f.Remediation != "" {
		s += " (repair: " + f.Remediation + ")"
	}
	return s
}

// HasErrors reports whether any finding is an error. Strict mode additionally
// promotes warnings via HasBlocking.
func HasErrors(findings []Finding) bool {
	for _, f := range findings {
		if f.Severity == SeverityError {
			return true
		}
	}
	return false
}

// HasBlocking is the gate decision: errors always block; warnings block only
// in strict mode.
func HasBlocking(findings []Finding, strict bool) bool {
	for _, f := range findings {
		if f.Severity == SeverityError || (strict && f.Severity == SeverityWarning) {
			return true
		}
	}
	return false
}
