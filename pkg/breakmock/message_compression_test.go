package breakmock

import "testing"

// Captured through HTTP-triggered fixed-channel messages on WireMock 4.0.0-beta.40.
func TestReferenceCompressedMessageBodies(t *testing.T) {
	store := NewStore().Messaging()
	for _, tc := range []struct {
		body        map[string]any
		field, want string
	}{
		{map[string]any{"data": "hello", "compression": "gzip"}, "body", "aGVsbG8="},
		{map[string]any{"data": "{{request.method}}", "compression": "gzip"}, "body", "e3tyZXF1ZXN0Lm1ldGhvZH19"},
		{map[string]any{"base64Data": "H4sIAAAAAAAC/8tIzcnJBwCGphA2BQAAAA==", "format": "text"}, "body", "H4sIAAAAAAAC/8tIzcnJBwCGphA2BQAAAA=="},
		{map[string]any{"data": "hello", "compression": "none"}, "body", "hello"},
		{map[string]any{"data": "hello", "compression": "gzip", "format": "binary"}, "base64Body", "aGVsbG8="},
	} {
		got, err := store.resolveMessageDefinition(map[string]any{"body": tc.body}, nil, &Request{Method: "GET"}, nil)
		if err != nil || got[tc.field] != tc.want {
			t.Fatalf("body=%v got=%v error=%v want=%q", tc.body, got, err, tc.want)
		}
	}
}
