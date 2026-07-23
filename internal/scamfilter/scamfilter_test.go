package scamfilter

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ptr[T any](v T) *T { return &v }

// TestScore_Fixtures scores the real catalogue poison from the 2026-07-14
// reconciliation and the three SPL scams from the 2026-07-20 Solana sync
// against clean majors. The fixtures are the acceptance criteria: every
// phishing name is condemned, every major stays legit.
func TestScore_Fixtures(t *testing.T) {
	cases := []struct {
		name        string
		in          Input
		wantVerdict Verdict
		wantSignal  string // a signal that must have fired ("" to skip)
	}{
		// --- Hard signals: terminal regardless of score ---
		{
			name:        "invisible separator in symbol",
			in:          Input{Symbol: "UNILP.NET\u2063", Name: "UNILP.NET"},
			wantVerdict: VerdictScam,
			wantSignal:  SignalInvisibleUnicode,
		},
		{
			name:        "cyrillic lookalike USDT",
			in:          Input{Symbol: "UЅDT", Name: "Tether USD"}, // U+0405 Cyrillic S
			wantVerdict: VerdictImpersonation,
			wantSignal:  SignalMixedScript,
		},
		{
			name:        "mixed-script Raydium clone",
			in:          Input{Symbol: "RAY", Name: "Rауdium аlрhа рrogram"},
			wantVerdict: VerdictImpersonation,
			wantSignal:  SignalMixedScript,
		},

		// --- Airdrop lures: accumulate to scam ---
		{
			name:        "aave airdrop lure",
			in:          Input{Symbol: "AAVE-SR", Name: "VISIT [AAVE-SR.XYZ] AND CLAIM SPECIAL REWARDS"},
			wantVerdict: VerdictScam, // domain + airdrop + overlong
			wantSignal:  SignalDomain,
		},
		{
			name:        "uni claim site",
			in:          Input{Symbol: "UNI", Name: "$ UNIClaimV2.com"},
			wantVerdict: VerdictScam, // domain + airdrop($, claim)
			wantSignal:  SignalAirdropLexicon,
		},

		// --- Corroborating provider signals ---
		{
			name: "unverified unlisted no text signal",
			in: Input{
				Symbol: "FOO", Name: "Foo Token",
				ContractVerified: ptr(false),
				HasPriceListing:  ptr(false),
			},
			wantVerdict: VerdictSuspect, // 0.2 + 0.3 = 0.5
		},
		{
			name: "provider spam alone",
			in: Input{
				Symbol: "BAR", Name: "Bar",
				ProviderSpam: ptr(true),
			},
			wantVerdict: VerdictSuspect, // 0.4
			wantSignal:  SignalProviderSpam,
		},

		// --- Legit majors: nothing fires ---
		{
			name:        "bitcoin",
			in:          Input{Symbol: "BTC", Name: "Bitcoin", ContractVerified: ptr(true), HasPriceListing: ptr(true)},
			wantVerdict: VerdictLegit,
		},
		{
			name:        "usdc verified listed",
			in:          Input{Symbol: "USDC", Name: "USD Coin", ContractVerified: ptr(true), HasPriceListing: ptr(true)},
			wantVerdict: VerdictLegit,
		},
		{
			name:        "wrapped ether",
			in:          Input{Symbol: "WETH", Name: "Wrapped Ether"},
			wantVerdict: VerdictLegit,
		},
		{
			// The mint calling itself "NFT" is structurally ordinary — decimals==0
			// is caught at the adapter, not here. Identity scoring leaves it legit.
			name:        "plain NFT-named token",
			in:          Input{Symbol: "NFT", Name: "NFT"},
			wantVerdict: VerdictLegit,
		},
	}

	w := DefaultWeights()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Score(tc.in, w)
			assert.Equal(t, tc.wantVerdict, got.Verdict,
				"score=%.2f signals=%v", got.Score, got.Signals)
			if tc.wantSignal != "" {
				assert.Contains(t, got.Signals, tc.wantSignal)
			}
		})
	}
}

// TestScore_NilSignalsDoNotContribute guards the tri-state contract: an unset
// provider signal must not push a clean asset toward suspicion.
func TestScore_NilSignalsDoNotContribute(t *testing.T) {
	got := Score(Input{Symbol: "DOT", Name: "Polkadot"}, DefaultWeights())
	assert.Equal(t, VerdictLegit, got.Verdict)
	assert.Zero(t, got.Score)
	assert.Empty(t, got.Signals)
}

// TestScore_HardSignalBeatsCleanContext ensures a hard signal condemns even when
// every soft context signal says legit — an invisible rune is not outvoted.
func TestScore_HardSignalBeatsCleanContext(t *testing.T) {
	got := Score(Input{
		Symbol:           "US\u200bDT", // zero-width space
		Name:             "Tether",
		ProviderSpam:     ptr(false),
		ContractVerified: ptr(true),
		HasPriceListing:  ptr(true),
	}, DefaultWeights())
	require.Equal(t, VerdictScam, got.Verdict)
	assert.Contains(t, got.Signals, SignalInvisibleUnicode)
}

// TestScore_ClampsAtOne verifies a pile of soft signals cannot exceed 1.
func TestScore_ClampsAtOne(t *testing.T) {
	got := Score(Input{
		Symbol:           "CLAIMREWARDNOW",             // overlong + airdrop
		Name:             "visit claim-rewards.xyz now", // domain + airdrop
		ProviderSpam:     ptr(true),
		ContractVerified: ptr(false),
		HasPriceListing:  ptr(false),
	}, DefaultWeights())
	assert.Equal(t, VerdictScam, got.Verdict)
	assert.LessOrEqual(t, got.Score, 1.0)
}
