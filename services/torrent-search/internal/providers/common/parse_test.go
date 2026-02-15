package common

import "testing"

func TestParseHumanSize(t *testing.T) {
	testCases := []struct {
		input string
		min   int64
	}{
		{input: "1.5 GB", min: 1500 * 1024 * 1024},
		{input: "700 MB", min: 700 * 1024 * 1024},
		{input: "1.2 ГБ", min: 1200 * 1024 * 1024},
	}
	for _, tc := range testCases {
		value := ParseHumanSize(tc.input)
		if value < tc.min {
			t.Fatalf("unexpected parsed size for %q: %d", tc.input, value)
		}
	}
}
