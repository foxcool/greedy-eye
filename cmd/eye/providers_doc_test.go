package main

import (
	"os"
	"strings"
	"testing"

	binanceadapter "github.com/foxcool/greedy-eye/internal/adapter/binance"
	blockchairadapter "github.com/foxcool/greedy-eye/internal/adapter/blockchair"
	"github.com/foxcool/greedy-eye/internal/adapter/coingecko"
	cosmosadapter "github.com/foxcool/greedy-eye/internal/adapter/cosmos"
	esploraadapter "github.com/foxcool/greedy-eye/internal/adapter/esplora"
	moralisadapter "github.com/foxcool/greedy-eye/internal/adapter/moralis"
	solanaadapter "github.com/foxcool/greedy-eye/internal/adapter/solana"
	subscanadapter "github.com/foxcool/greedy-eye/internal/adapter/subscan"
	tonapiadapter "github.com/foxcool/greedy-eye/internal/adapter/tonapi"
	tzktadapter "github.com/foxcool/greedy-eye/internal/adapter/tzkt"
)

// TestProvidersAreDocumented keeps docs/providers.md honest.
//
// A provider is only reachable through an account naming its slug, so an
// undocumented adapter is a capability nobody can discover or configure — it
// exists in the binary and nowhere else. This fails the build instead of
// letting the catalogue drift, which is the failure mode a hand-written
// reference has.
//
// It checks presence, not correctness: chains, key requirements and quirks
// still need a human. Adding an adapter means adding its row.
func TestProvidersAreDocumented(t *testing.T) {
	doc, err := os.ReadFile("../../docs/providers.md")
	if err != nil {
		t.Fatalf("read provider docs: %v", err)
	}
	text := string(doc)

	slugs := []string{
		moralisadapter.ProviderName,
		subscanadapter.ProviderName,
		tonapiadapter.ProviderName,
		solanaadapter.ProviderName,
		esploraadapter.ProviderName,
		cosmosadapter.ProviderName,
		tzktadapter.ProviderName,
		blockchairadapter.ProviderName,
		coingecko.ProviderName,
		binanceadapter.ProviderName,
	}

	for _, slug := range slugs {
		if !strings.Contains(text, "`"+slug+"`") {
			t.Errorf("provider %q is registered but missing from docs/providers.md", slug)
		}
	}
}

// TestDocumentedChainsMatchAdapters pins the chain lists in the table. These
// are what a user types into an account's chain field, so a stale list here
// sends them to a chain the adapter will reject.
func TestDocumentedChainsMatchAdapters(t *testing.T) {
	doc, err := os.ReadFile("../../docs/providers.md")
	if err != nil {
		t.Fatalf("read provider docs: %v", err)
	}
	text := string(doc)

	multiChain := [][]string{
		subscanadapter.SupportedChains(),
		cosmosadapter.SupportedChains(),
		blockchairadapter.SupportedChains(),
		solanaadapter.SupportedChains(),
		esploraadapter.SupportedChains(),
		tzktadapter.SupportedChains(),
		tonapiadapter.SupportedChains(),
	}

	for _, chains := range multiChain {
		for _, chain := range chains {
			if !strings.Contains(text, chain) {
				t.Errorf("chain %q is supported but missing from docs/providers.md", chain)
			}
		}
	}
}
