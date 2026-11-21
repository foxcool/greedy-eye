package entity

import (
	"strings"
	"testing"
)

func TestCanonicalURL(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"lower-cases scheme and host", "HTTPS://Example.COM/News/A", "https://example.com/News/A"},
		{"drops fragment", "https://example.com/a#comments", "https://example.com/a"},
		{"drops default port", "https://example.com:443/a", "https://example.com/a"},
		{"keeps other port", "http://example.com:8080/a", "http://example.com:8080/a"},
		{"drops tracking params, sorts the rest", "https://example.com/a?utm_source=rss&b=2&fbclid=x&a=1", "https://example.com/a?a=1&b=2"},
		{"empty path is root", "https://example.com", "https://example.com/"},
		{"trims space", "  https://example.com/a \n", "https://example.com/a"},
		{"keeps IPv6 brackets with a port", "https://[::1]:8443/a", "https://[::1]:8443/a"},
		{"keeps IPv6 brackets without a port", "https://[2001:db8::1]/a", "https://[2001:db8::1]/a"},
		{"keeps IPv6 brackets, drops default port", "https://[2001:DB8::1]:443/a", "https://[2001:db8::1]/a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CanonicalURL(tc.in)
			if err != nil {
				t.Fatalf("CanonicalURL(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("CanonicalURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestCanonicalURLRefuses(t *testing.T) {
	for _, in := range []string{"", "javascript:alert(1)", "ftp://example.com/a", "/relative/path", "https://"} {
		if got, err := CanonicalURL(in); err == nil {
			t.Errorf("CanonicalURL(%q) = %q, want an error", in, got)
		}
	}
}

func TestURLHashSameArticleSameHash(t *testing.T) {
	a, err := URLHash("https://Example.com/a?utm_medium=feed#top")
	if err != nil {
		t.Fatal(err)
	}
	b, err := URLHash("https://example.com/a")
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("hashes differ for one article: %s vs %s", a, b)
	}
	c, _ := URLHash("https://example.com/b")
	if a == c {
		t.Fatal("two articles share a hash")
	}
}

func TestTruncateRunes(t *testing.T) {
	if got := TruncateRunes("short", 10); got != "short" {
		t.Fatalf("got %q", got)
	}
	long := strings.Repeat("я", 20)
	got := TruncateRunes(long, 10)
	if n := len([]rune(got)); n != 10 {
		t.Fatalf("got %d runes, want 10: %q", n, got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("cut is not marked: %q", got)
	}
}
