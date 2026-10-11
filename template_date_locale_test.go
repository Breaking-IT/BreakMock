package breakmock

import (
	"testing"
	"time"
)

// Expected bytes were observed in WireMock 4.0.0-beta.40 on OpenJDK 27.
func TestReferenceDateLocaleFormatting(t *testing.T) {
	date := time.Date(2020, 2, 3, 1, 0, 0, 0, time.FixedZone("CET", 3600))
	for _, test := range []struct{ locale, style, clock, want string }{
		{"en_GB", "short", "", "03/02/2020"},
		{"en_US", "full", "", "Monday, February 3, 2020"},
		{"en_US", "medium", "short", "Feb 3, 2020, 1:00\u202fAM"},
		{"en_US", "medium", "long", "Feb 3, 2020, 1:00:00\u202fAM CET"},
		{"de_DE", "medium", "", "03.02.2020"},
		{"fr_FR", "medium", "", "3 févr. 2020"},
		{"de_DE", "short", "", "03.02.20"},
	} {
		t.Run(test.locale+"/"+test.style+"/"+test.clock, func(t *testing.T) {
			if got := templateLocalizedDate(date, test.style, test.clock, test.locale); got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}
