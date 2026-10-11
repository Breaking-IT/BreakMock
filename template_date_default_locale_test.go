package breakmock

import (
	"testing"
	"time"
)

func TestDateDefaultLocaleAMPM(t *testing.T) {
	previous := templateDefaultLocale()
	t.Cleanup(func() { templateLocale = previous })
	for _, tc := range []struct{ locale, period string }{{"en_US", "PM"}, {"en_NL", "pm"}} {
		templateLocale = tc.locale
		date := time.Date(2026, 1, 2, 15, 4, 0, 0, time.UTC)
		if got := templateFormatDate(date, "a"); got != tc.period {
			t.Fatalf("%s: got %q want %q", tc.locale, got, tc.period)
		}
		_, _, err := matcherDateParse("02 January 2026 at 03:04 "+tc.period, "dd MMMM yyyy 'at' hh:mm a")
		if err != nil {
			t.Fatalf("%s: %v", tc.locale, err)
		}
		other := "PM"
		if tc.period == "PM" {
			other = "pm"
		}
		if _, _, err := matcherDateParse("02 January 2026 at 03:04 "+other, "dd MMMM yyyy 'at' hh:mm a"); err == nil {
			t.Fatalf("%s accepted wrong AM/PM case %q", tc.locale, other)
		}
	}
}
