package providerstore

import "testing"

func TestCanonicalSourceURLV1(t *testing.T) {
	tests := []struct {
		raw  string
		want string
		good bool
	}{
		{raw: "https://example.com/a", want: "https://example.com/a", good: true},
		{raw: "https://example.com", want: "https://example.com/", good: true},
		{raw: "https://[2001:0db8:0:0:0:0:0:1]/archive", want: "https://[2001:db8::1]/archive", good: true},
		{raw: "https://example.com/%2B", want: "https://example.com/%2B", good: true},
		{raw: "http://example.com/a"},
		{raw: "https://EXAMPLE.com/a"},
		{raw: "https://example.com:443/a"},
		{raw: "https://user@example.com/a"},
		{raw: "https://example.com/a?query"},
		{raw: "https://example.com/a#fragment"},
		{raw: "https://example.com/%2b"},
		{raw: "https://example.com/../a"},
		{raw: "https://127.0.0.1/a", want: "https://127.0.0.1/a", good: true},
	}
	for _, test := range tests {
		t.Run(test.raw, func(t *testing.T) {
			got, err := CanonicalSourceURLV1(test.raw)
			if test.good {
				if err != nil || got != test.want {
					t.Fatalf("CanonicalSourceURLV1(%q) = %q, %v; want %q", test.raw, got, err, test.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("CanonicalSourceURLV1(%q) = %q, want error", test.raw, got)
			}
		})
	}
}
