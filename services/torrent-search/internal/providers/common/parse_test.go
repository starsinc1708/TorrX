package common

import "testing"

// ---------------------------------------------------------------------------
// ParseHumanSize
// ---------------------------------------------------------------------------

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

func TestParseHumanSizeAllUnits(t *testing.T) {
	cases := []struct {
		input string
		want  int64
	}{
		{"1 B", 1},
		{"1 KB", 1024},
		{"1 MB", 1024 * 1024},
		{"1 GB", 1024 * 1024 * 1024},
		{"1 TB", 1024 * 1024 * 1024 * 1024},
	}
	for _, tc := range cases {
		got := ParseHumanSize(tc.input)
		if got != tc.want {
			t.Errorf("ParseHumanSize(%q) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

func TestParseHumanSizeFractional(t *testing.T) {
	cases := []struct {
		input string
		min   int64
		max   int64
	}{
		{"1.5 GB", 1610612736 - 1, 1610612736 + 1}, // 1.5 * 1024^3
		{"2.5 MB", 2621440 - 1, 2621440 + 1},       // 2.5 * 1024^2
		{"0.5 KB", 511, 513},                         // 0.5 * 1024
	}
	for _, tc := range cases {
		got := ParseHumanSize(tc.input)
		if got < tc.min || got > tc.max {
			t.Errorf("ParseHumanSize(%q) = %d, want between %d and %d", tc.input, got, tc.min, tc.max)
		}
	}
}

func TestParseHumanSizeCyrillic(t *testing.T) {
	cases := []struct {
		input string
		min   int64
	}{
		{"500 МБ", 500 * 1024 * 1024},
		{"2 ГБ", 2 * 1024 * 1024 * 1024},
		{"100 КБ", 100 * 1024},
		{"1 ТБ", 1024 * 1024 * 1024 * 1024},
	}
	for _, tc := range cases {
		got := ParseHumanSize(tc.input)
		if got < tc.min {
			t.Errorf("ParseHumanSize(%q) = %d, want >= %d", tc.input, got, tc.min)
		}
	}
}

func TestParseHumanSizeCommaDecimal(t *testing.T) {
	got := ParseHumanSize("1,5 GB")
	min := int64(1610612736 - 1) // 1.5 * 1024^3
	if got < min {
		t.Errorf("ParseHumanSize(\"1,5 GB\") = %d, want >= %d", got, min)
	}
}

func TestParseHumanSizeEmpty(t *testing.T) {
	got := ParseHumanSize("")
	if got != 0 {
		t.Errorf("ParseHumanSize(\"\") = %d, want 0", got)
	}
}

func TestParseHumanSizeWhitespace(t *testing.T) {
	got := ParseHumanSize("   ")
	if got != 0 {
		t.Errorf("ParseHumanSize(whitespace) = %d, want 0", got)
	}
}

func TestParseHumanSizeNoUnit(t *testing.T) {
	got := ParseHumanSize("12345")
	if got != 12345 {
		t.Errorf("ParseHumanSize(\"12345\") = %d, want 12345", got)
	}
}

func TestParseHumanSizeInvalid(t *testing.T) {
	got := ParseHumanSize("abc GB")
	if got != 0 {
		t.Errorf("ParseHumanSize(\"abc GB\") = %d, want 0", got)
	}
}

func TestParseHumanSizeNegative(t *testing.T) {
	got := ParseHumanSize("-5 MB")
	if got != 0 {
		t.Errorf("ParseHumanSize(\"-5 MB\") = %d, want 0", got)
	}
}

func TestParseHumanSizeZero(t *testing.T) {
	got := ParseHumanSize("0 MB")
	if got != 0 {
		t.Errorf("ParseHumanSize(\"0 MB\") = %d, want 0", got)
	}
}

func TestParseHumanSizeCaseInsensitive(t *testing.T) {
	// The function uses ToUpper so lowercase should work
	cases := []struct {
		input string
		min   int64
	}{
		{"1 gb", 1024 * 1024 * 1024},
		{"100 mb", 100 * 1024 * 1024},
		{"10 kb", 10 * 1024},
	}
	for _, tc := range cases {
		got := ParseHumanSize(tc.input)
		if got < tc.min {
			t.Errorf("ParseHumanSize(%q) = %d, want >= %d", tc.input, got, tc.min)
		}
	}
}

// ---------------------------------------------------------------------------
// CleanHTMLText
// ---------------------------------------------------------------------------

func TestCleanHTMLTextBasic(t *testing.T) {
	got := CleanHTMLText("<b>Hello</b> <i>World</i>")
	if got != "Hello World" {
		t.Errorf("CleanHTMLText: got %q, want %q", got, "Hello World")
	}
}

func TestCleanHTMLTextEmpty(t *testing.T) {
	got := CleanHTMLText("")
	if got != "" {
		t.Errorf("CleanHTMLText(\"\") = %q, want empty", got)
	}
}

func TestCleanHTMLTextWhitespace(t *testing.T) {
	got := CleanHTMLText("   hello   world   ")
	if got != "hello world" {
		t.Errorf("CleanHTMLText: got %q, want %q", got, "hello world")
	}
}

func TestCleanHTMLTextHTMLEntities(t *testing.T) {
	// &amp; is unescaped to &, &lt;test&gt; is unescaped to <test> then stripped as a tag
	got := CleanHTMLText("Hello &amp; World &lt;test&gt;")
	if got != "Hello & World" {
		t.Errorf("CleanHTMLText: got %q, want %q", got, "Hello & World")
	}
}

func TestCleanHTMLTextAmpersandEntity(t *testing.T) {
	got := CleanHTMLText("Tom &amp; Jerry")
	if got != "Tom & Jerry" {
		t.Errorf("CleanHTMLText: got %q, want %q", got, "Tom & Jerry")
	}
}

func TestCleanHTMLTextNestedTags(t *testing.T) {
	got := CleanHTMLText("<div><span>Nested</span> <a href='#'>Content</a></div>")
	if got != "Nested Content" {
		t.Errorf("CleanHTMLText: got %q, want %q", got, "Nested Content")
	}
}

func TestCleanHTMLTextNoTags(t *testing.T) {
	got := CleanHTMLText("Just plain text")
	if got != "Just plain text" {
		t.Errorf("CleanHTMLText: got %q, want %q", got, "Just plain text")
	}
}

func TestCleanHTMLTextMultipleSpacesAfterTagRemoval(t *testing.T) {
	got := CleanHTMLText("<br><br><br>text<br><br>")
	if got != "text" {
		t.Errorf("CleanHTMLText: got %q, want %q", got, "text")
	}
}
