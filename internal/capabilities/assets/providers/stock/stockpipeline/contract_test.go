package stockpipeline

import "testing"

func TestValidateDurationContract(t *testing.T) {
	if err := ValidateDurationContract(60, 20, 5, 4, "sections_only"); err != nil {
		t.Fatalf("valid smoke contract rejected: %v", err)
	}
	if err := ValidateDurationContract(600, 600, 15, 40, "sections_only"); err != nil {
		t.Fatalf("40-second, 15-source pilot contract rejected: %v", err)
	}
	for name, args := range map[string][5]interface{}{
		"ambiguous mode":   {60, 20, 5, 4, "full"},
		"wrong per source": {60, 60, 5, 4, "sections_only"},
		"missing field":    {60, 20, 0, 4, "sections_only"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateDurationContract(args[0].(int), args[1].(int), args[2].(int), args[3].(int), args[4].(string)); err == nil {
				t.Fatal("expected duration contract error")
			}
		})
	}
}
