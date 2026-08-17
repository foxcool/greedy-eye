package provider

import (
	"os"
	"strings"
	"testing"
)

// TestProvidersAreDocumented keeps docs/providers.md honest.
//
// A provider is only reachable through an account naming its slug, so an
// undocumented adapter is a capability nobody can discover or configure — it
// exists in the binary and nowhere else. This fails the build instead of
// letting the reference drift, which is the failure mode a hand-written
// document has.
//
// It reads the catalogue rather than a list of its own: a provider added to the
// registry now arrives here without anyone remembering to add it.
//
// It checks presence, not correctness: quirks and tariffs still need a human.
func TestProvidersAreDocumented(t *testing.T) {
	text := providerDocs(t)

	for _, d := range describeAll() {
		if !strings.Contains(text, "`"+d.Slug+"`") {
			t.Errorf("provider %q is registered but missing from docs/providers.md", d.Slug)
		}
	}
}

// TestDocumentedChainsMatchAdapters pins the chain lists in the table. These
// are what a user types into an account's chain field, so a stale list here
// sends them to a chain the adapter will reject.
func TestDocumentedChainsMatchAdapters(t *testing.T) {
	text := providerDocs(t)

	for _, d := range describeAll() {
		for _, chain := range d.Chains {
			if !strings.Contains(text, chain) {
				t.Errorf("chain %q is supported by %s but missing from docs/providers.md", chain, d.Slug)
			}
		}
	}
}

func providerDocs(t *testing.T) string {
	t.Helper()
	doc, err := os.ReadFile("../../docs/providers.md")
	if err != nil {
		t.Fatalf("read provider docs: %v", err)
	}
	return string(doc)
}
