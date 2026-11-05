package ftdivbus

import "testing"

func TestNormalizeSerial(t *testing.T) {
	cases := map[string]string{
		"  DK0MAQPC  ": "DK0MAQPC",
		"dk0maqpc":     "DK0MAQPC",
		"DK0MAQPCA":    "DK0MAQPC", // FTDIBUS interface-letter suffix
		"":             "",
		"AB":           "AB",
	}
	for in, want := range cases {
		if got := normalizeSerial(in); got != want {
			t.Errorf("normalizeSerial(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCBusMask(t *testing.T) {
	cases := []struct {
		pin  int
		high bool
		want byte
	}{
		{0, true, 0x11},
		{0, false, 0x10},
		{1, true, 0x22},
		{3, false, 0x80},
	}
	for _, c := range cases {
		if got := cbusMask(c.pin, true, c.high); got != c.want {
			t.Errorf("cbusMask(%d, true, %v) = 0x%02X, want 0x%02X", c.pin, c.high, got, c.want)
		}
	}
}

func TestLevelMapping(t *testing.T) {
	// active-low: pin low (false) turns VBUS on.
	if levelFor(true, true) {
		t.Error("active-low VBUS on should drive the pin low")
	}
	if vbusFromLevel(true, false) != true {
		t.Error("active-low pin low should read back as VBUS on")
	}
	// active-high is the inverse.
	if !levelFor(false, true) {
		t.Error("active-high VBUS on should drive the pin high")
	}
	if vbusFromLevel(false, true) != true {
		t.Error("active-high pin high should read back as VBUS on")
	}
}
