package scriptgeneration

import "testing"

func TestScratchExpandPersonName2(t *testing.T) {
	cases := []struct{ text, value string }{
		{"Elon Musk founded Tesla.", "Musk"},
		{"Elon Musk founded Tesla.", "Elon"},
		{"Dwayne Johnson walked into Miami.", "Johnson"},
		{"Elon Musk founded Tesla. Later Musk sold shares.", "Musk"},
		{"Michael Jordan played for the Chicago Bulls.", "Jordan"},
		{"Beyonce performed in London.", "Beyonce"},
		{`Dwayne "The Rock" Johnson arrived.`, "Rock"},
		{"President Obama spoke.", "Obama"},
	}
	for _, c := range cases {
		t.Logf("text=%q value=%q => %q", c.text, c.value, expandPersonName(c.text, c.value))
	}
}
