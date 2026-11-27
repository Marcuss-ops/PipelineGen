package certification

import "encoding/json"

// Marshal encodes the canonical document with a stable indentation and a
// trailing newline. It deliberately does not stamp a time: two certifications
// of the same evidence must be byte-identical.
func Marshal(report *Report) ([]byte, error) {
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}
