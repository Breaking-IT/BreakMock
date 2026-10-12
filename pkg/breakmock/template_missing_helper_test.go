package breakmock

import "testing"

func TestReferenceUnknownHelperDiagnostics(t *testing.T) {
	for _, tc := range []struct{ source, want string }{
		{"{{unknown 1}}", "1:2: could not find helper: 'unknown'\n{{unknown 1}}\n  ^\n"},
		{"prefix {{unknown foo=1}}", "1:2: could not find helper: 'unknown'\n{{unknown foo=1}}\n  ^\n"},
		{"{{val (unknown 1)}}", "1:7: could not find helper: 'unknown'\n{{val (unknown 1)}}\n       ^\n"},
		{"{{#if false}}{{unknown 1}}{{/if}}", "1:15: could not find helper: 'unknown'\n{{#if false}}{{unknown 1}}{{/if}}\n               ^\n"},
		{"{{#unknown 1}}x{{/unknown}}", "java.lang.IllegalArgumentException: could not find helper: 'unknown'"},
		{"{{#if true}}\n  {{unknown 1}}\n{{/if}}", "2:4: could not find helper: 'unknown'\n  {{unknown 1}}\n    ^\n"},
	} {
		t.Run(tc.source, func(t *testing.T) {
			_, err := RenderTemplate(tc.source, Request{URL: "/"}, nil)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
	for _, source := range []string{"{{unknown}}", "{{#unknown}}x{{/unknown}}"} {
		got, err := RenderTemplate(source, Request{URL: "/"}, nil)
		if err != nil || got != "" {
			t.Fatalf("%s got %q %v", source, got, err)
		}
	}
	_, err := RenderMessageTemplateWithRequest("{{unknown 1}}", nil, nil, nil)
	want := "inline@543fa0a5:1:2: could not find helper: 'unknown'\n{{unknown 1}}\n  ^\n"
	if err == nil || err.Error() != want {
		t.Fatalf("message got %v, want %q", err, want)
	}
}
