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
		{
			// The dev catalogue's 1.89M "USDT" off 0x7f1ffe63 (personal-go65):
			// plain ASCII, right length, nothing in the text to catch. Before the
			// collision signal it scored 0.2 — legit — while the catalogue knew
			// Tether holds that ticker on eth from another contract.
			name:        "exact ticker off a foreign contract",
			in:          Input{Symbol: "USDT", Name: "USDT", ClaimsHeldTicker: true},
			wantVerdict: VerdictImpersonation,
			wantSignal:  SignalTickerCollision,
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

		// --- Listed venues: the exchange spells its own instruments ---
		// The four MOEX positions the mixed-script rule wrote out of the sum on
		// the first live broker sync (personal-42s8). Each mixes scripts the way
		// the broker's catalogue does: a Latin brand beside Russian words, and a
		// bond series whose "P" is Latin inside a Cyrillic word.
		{
			name:        "FinEx equity fund on moex",
			in:          Input{Symbol: "FXRL", Name: "FinEx Акции российских компаний", VenueListed: true},
			wantVerdict: VerdictLegit,
		},
		{
			name:        "FinEx eurobond fund on moex",
			in:          Input{Symbol: "FXRU", Name: "FinEx Еврооблигации рос. компаний (USD)", VenueListed: true},
			wantVerdict: VerdictLegit,
		},
		{
			name:        "Russian Post bond series",
			in:          Input{Symbol: "RU000A1055Y4", Name: "Почта России БО-002P-04", VenueListed: true},
			wantVerdict: VerdictLegit,
		},
		{
			// Latin "O" inside Cyrillic "БO" — the exact confusable the rule hunts,
			// spelled that way by the venue itself.
			name:        "Novye Tekhnologii bond series",
			in:          Input{Symbol: "RU000A105DL4", Name: "Новые Технологии БO-01", VenueListed: true},
			wantVerdict: VerdictLegit,
		},
		{
			// The same name off a venue is the attack again. This pair is the
			// whole fix: the text did not change, the identity behind it did.
			name:        "same mixed-script name with no venue behind it",
			in:          Input{Symbol: "FXRL", Name: "FinEx Акции российских компаний"},
			wantVerdict: VerdictImpersonation,
			wantSignal:  SignalMixedScript,
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

// TestScore_TickerCollisionBeatsCleanContext: the shape a lookalike is built to
// have — verified contract, listed, provider says nothing — must not reprieve it.
// The real Tether, whose ticker nobody else holds, stays legit under the same
// context.
func TestScore_TickerCollisionBeatsCleanContext(t *testing.T) {
	clean := Input{
		Symbol:           "USDT",
		Name:             "Tether",
		ProviderSpam:     ptr(false),
		ContractVerified: ptr(true),
		HasPriceListing:  ptr(true),
	}
	assert.Equal(t, VerdictLegit, Score(clean, DefaultWeights()).Verdict)

	claiming := clean
	claiming.ClaimsHeldTicker = true
	got := Score(claiming, DefaultWeights())
	require.Equal(t, VerdictImpersonation, got.Verdict)
	assert.Contains(t, got.Signals, SignalTickerCollision)
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

// TestScore_VenueListedKeepsWeightedSignals: disarming the text hard signals is
// not a blanket amnesty. A listed instrument still accumulates the weighted
// signals, and the structural collision still condemns it — only the spelling
// stops being evidence.
func TestScore_VenueListedKeepsWeightedSignals(t *testing.T) {
	t.Run("weighted signals still accumulate", func(t *testing.T) {
		got := Score(Input{
			Symbol:      "FOO",
			Name:        "visit claim-rewards.xyz now",
			VenueListed: true,
		}, DefaultWeights())
		assert.Equal(t, VerdictScam, got.Verdict, "signals=%v", got.Signals)
		assert.Contains(t, got.Signals, SignalDomain)
		assert.Contains(t, got.Signals, SignalAirdropLexicon)
	})

	t.Run("ticker collision still condemns", func(t *testing.T) {
		got := Score(Input{
			Symbol: "USDT", Name: "Tether", VenueListed: true, ClaimsHeldTicker: true,
		}, DefaultWeights())
		require.Equal(t, VerdictImpersonation, got.Verdict)
		assert.Contains(t, got.Signals, SignalTickerCollision)
	})

	t.Run("invisible runes are not evidence on a venue", func(t *testing.T) {
		// A venue catalogue does not carry zero-width joiners, but if one turns
		// up it is a transport artefact, not a smuggled lookalike: the position
		// binds by FIGI and the spelling is the exchange's own.
		got := Score(Input{
			Symbol: "SBER\u200b", Name: "Сбербанк", VenueListed: true,
		}, DefaultWeights())
		assert.Equal(t, VerdictLegit, got.Verdict, "signals=%v", got.Signals)
		assert.NotContains(t, got.Signals, SignalInvisibleUnicode)
	})
}
