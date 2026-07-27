package adapters

import "testing"

func TestParseTypedEntity(t *testing.T) {
	kind, value := parseTypedEntity("PERSON: Ada Lovelace")
	if kind != "PERSON" || value != "Ada Lovelace" {
		t.Fatalf("unexpected parsed entity: %s %s", kind, value)
	}

	kind, value = parseTypedEntity("[PLACE] Rome")
	if kind != "PLACE" || value != "Rome" {
		t.Fatalf("unexpected parsed place: %s %s", kind, value)
	}

	kind, value = parseTypedEntity("Apollo 11")
	if kind != "CONCEPT" || value != "Apollo 11" {
		t.Fatalf("unexpected legacy entity: %s %s", kind, value)
	}
}
