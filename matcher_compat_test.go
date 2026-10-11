package breakmock

import (
	"encoding/json"
	"math"
	"os"
	"testing"
)

func TestJavaMatcherFixtures(t *testing.T) {
	raw, err := os.ReadFile("testdata/matcher_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name    string
		Pattern map[string]any
		Body    string
		Matches bool
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			if got := MatchRequest(c.Pattern, Request{Method: "POST", URL: "/test", Body: []byte(c.Body)}); got != c.Matches {
				t.Fatalf("got %v, Java reference %v", got, c.Matches)
			}
		})
	}
}
func TestJavaDistanceFixtures(t *testing.T) {
	raw, err := os.ReadFile("testdata/matcher_distance_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name     string
		Pattern  map[string]any
		Actual   string
		Distance float64
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			got := RequestMatchDistance(map[string]any{"bodyPatterns": []any{c.Pattern}}, Request{Method: "POST", URL: "/", Body: []byte(c.Actual)})
			if math.Abs(got-c.Distance) > 1e-15 {
				t.Fatalf("got %.17g, Java %.17g", got, c.Distance)
			}
		})
	}
}

func TestJavaMessageFixtures(t *testing.T) {
	raw, err := os.ReadFile("testdata/matcher_message_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name                     string
		Pattern                  map[string]any
		Body                     string
		Present, Binary, Matches bool
		Channel                  map[string]any
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			var channel *Request
			if c.Channel != nil {
				r := Request{Method: bmString(c.Channel["method"]), URL: bmString(c.Channel["url"])}
				channel = &r
			}
			f := MatchMessage
			if c.Binary {
				f = MatchBinaryMessage
			}
			if got := f(c.Pattern, channel, []byte(c.Body), c.Present); got != c.Matches {
				t.Fatalf("got %v Java %v", got, c.Matches)
			}
		})
	}
}
