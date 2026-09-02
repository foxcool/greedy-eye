// Package scamfilter scores an asset's identity: whether it is what it claims
// to be, or a scam clone, an impersonation of a known ticker, or an airdrop
// lure. It is axis 1 of the catalogue's risk model — a permanent property of
// the asset, distinct from a real asset's situational risk (exploit, depeg,
// freeze) and from a user's per-holding accounting decision (holdings.excluded).
//
// The scorer is a pure function over what is cheaply knowable at sync intake or
// during a rescoring pass: the symbol and name text, plus a few context signals
// a provider may or may not report. It replaces the interim per-adapter drops
// (moralis possible_spam/unverified, solana isJunk) with an explainable score
// and a verdict that the sync path acts on — quarantine instead of silent drop,
// so a scam position keeps syncing but stays out of the sums.
//
// Not every signal is in its domain everywhere: a rule bought for
// attacker-controlled token names is false over an exchange's own catalogue, so
// Input carries the context that disarms it. See VenueListed.
//
// Weights and thresholds live in Weights so they can be tuned from config
// without a release; DefaultWeights holds the starting point from the design
// note (mind_synced/projects/greedy-eye/scam-filtering.md).
package scamfilter

import (
	"regexp"
	"strings"
	"unicode"
)

// Verdict is the identity judgement for an asset. It is terminal for scam and
// impersonation (both derive holdings.excluded); suspect is a review state that
// a human confirms; legit and unknown stay in the sums.
type Verdict string

const (
	// VerdictUnknown is the pre-scoring default: never judged.
	VerdictUnknown Verdict = "unknown"
	// VerdictLegit is a scored asset below the suspect threshold.
	VerdictLegit Verdict = "legit"
	// VerdictSuspect is auto-flagged for human review; not yet excluded.
	VerdictSuspect Verdict = "suspect"
	// VerdictScam is confirmed junk by score or hard signal; excluded from sums.
	VerdictScam Verdict = "scam"
	// VerdictImpersonation is a lookalike of a real ticker (mixed-script
	// confusables); excluded, and distinct from scam because the danger is that
	// it merges into the genuine asset rather than that it is worthless.
	VerdictImpersonation Verdict = "impersonation"
)

// Signal names as they appear in identity_signals jsonb, kept stable for
// explainability in the UI and for weight tuning by name.
const (
	SignalInvisibleUnicode = "invisible_unicode"
	SignalMixedScript      = "mixed_script"
	SignalDomain           = "domain"
	SignalAirdropLexicon   = "airdrop_lexicon"
	SignalOverlong         = "overlong"
	SignalProviderSpam     = "provider_spam"
	SignalUnverified       = "unverified_contract"
	SignalNoListing        = "no_listing"
	SignalTickerCollision  = "ticker_collision"
)

// Weights sets each signal's contribution to the score and the verdict cutoffs.
// A zero Weights scores nothing; callers pass DefaultWeights (or a config
// overlay). Hard signals (invisible Unicode, mixed script) bypass the score and
// set a verdict directly, so they carry no weight here.
type Weights struct {
	Domain           float64
	AirdropLexicon   float64
	ProviderSpam     float64
	Unverified       float64
	NoListing        float64
	Overlong         float64
	ScamThreshold    float64
	SuspectThreshold float64
}

// DefaultWeights is the starting calibration from the design note. Tuning
// happens in config, not here; these are the values the fixtures are scored
// against.
func DefaultWeights() Weights {
	return Weights{
		Domain:           0.5,
		AirdropLexicon:   0.4,
		ProviderSpam:     0.4,
		Unverified:       0.2,
		NoListing:        0.3,
		Overlong:         0.2,
		ScamThreshold:    0.8,
		SuspectThreshold: 0.4,
	}
}

// Input is what the scorer sees about one asset. The three context signals are
// pointers because "the provider did not report this" (non-EVM chains carry no
// contract-verification bit, an asset may never have been price-resolved yet) is
// distinct from a reported false: a nil never contributes to the score.
type Input struct {
	Symbol string
	Name   string
	// ProviderSpam is a source's own spam flag (moralis possible_spam).
	ProviderSpam *bool
	// ContractVerified is a source's contract-verification bit; a reported
	// false contributes, an unset (nil) does not — most majors are verified.
	ContractVerified *bool
	// HasPriceListing is whether any price provider has ever resolved this
	// asset; a reported false (no listing after N fetches) is a weak scam
	// signal, an unset does not judge a freshly-seen asset.
	HasPriceListing *bool
	// ClaimsHeldTicker is whether this asset's ticker is already held, on one of
	// its own chains, by an older price-listed asset bound to a different
	// contract. The catalogue answers it; the scorer only judges it. Text cannot
	// see this shape at all — the whole point of a lookalike is that its symbol
	// is spelled exactly right.
	ClaimsHeldTicker bool
	// VenueListed marks an instrument whose identity comes from an exchange's
	// catalogue — a listing on moex, spbex, nasdaq, bound by a FIGI — rather
	// than from a name its issuer chose. It disarms the text hard signals; see
	// Score.
	VenueListed bool
}

// Result is the scored outcome. Signals maps each contributing signal to its
// value, so the UI can show why an asset was judged and a human can override.
type Result struct {
	Score   float64
	Verdict Verdict
	Signals map[string]float64
}

// Score judges an asset's identity. Hard signals short-circuit to a terminal
// verdict; otherwise the weighted signals accumulate into a 0..1 score mapped
// to scam/suspect/legit by the thresholds. Signals is always populated with
// what fired, including the hard signal, for explainability.
func Score(in Input, w Weights) Result {
	signals := map[string]float64{}

	// Hard signals over the TEXT: shapes that cannot be an accident when the
	// text is the issuer's own choice. Invisible or control runes in a ticker (a
	// zero-width joiner, an RTL override) exist only to smuggle a lookalike past
	// the eye — scam outright. Mixed Latin/Cyrillic in one name is how a clone
	// impersonates a real project. Both bypass the score: no accumulation of
	// weak signals should be needed to condemn them, and none should be able to
	// reprieve them.
	//
	// They are silent on a listed venue, because there the premise is false. An
	// exchange's catalogue spells its own instruments, and the position binds to
	// the venue's identifier rather than to the spelling, so a Latin brand
	// beside Russian words ("FinEx Акции российских компаний") or a Latin P
	// inside a Cyrillic bond series ("БО-002P-04") is the catalogue being itself,
	// not an attack. Firing there condemned four real MOEX positions out of the
	// sum (personal-42s8). The rule was bought for crypto, where the name is
	// attacker-controlled and mixed script has no innocent use (personal-go65),
	// and it keeps every bit of that force there.
	if !in.VenueListed {
		if hasInvisibleRune(in.Symbol) || hasInvisibleRune(in.Name) {
			signals[SignalInvisibleUnicode] = 1
			return Result{Score: 1, Verdict: VerdictScam, Signals: signals}
		}
		if hasMixedScript(in.Symbol) || hasMixedScript(in.Name) {
			signals[SignalMixedScript] = 1
			return Result{Score: 1, Verdict: VerdictImpersonation, Signals: signals}
		}
	}
	// A ticker already held on this chain by an older, listed asset with another
	// contract. Hard for the same reason as mixed script: minting a second
	// contract under a taken ticker on the same chain is not something a real
	// project does by accident. It has to be hard rather than weighted, because
	// the text of a good lookalike fires nothing — a plain ASCII "USDT" of normal
	// length reaches 0.2, and even with no_listing it tops out at 0.5, under the
	// 0.8 that would condemn it. No accumulation of weak signals can ever judge
	// this shape (personal-go65).
	//
	// Unconditional, unlike the text signals above: it judges a structure the
	// chain enforces, not a spelling anyone chose, so a listing elsewhere says
	// nothing about it. A tokenized share is not excused from it by being listed.
	if in.ClaimsHeldTicker {
		signals[SignalTickerCollision] = 1
		return Result{Score: 1, Verdict: VerdictImpersonation, Signals: signals}
	}

	score := 0.0
	add := func(name string, weight float64) {
		signals[name] = weight
		score += weight
	}

	if looksLikeDomain(in.Symbol) || looksLikeDomain(in.Name) {
		add(SignalDomain, w.Domain)
	}
	if hasAirdropLexicon(in.Symbol) || hasAirdropLexicon(in.Name) {
		add(SignalAirdropLexicon, w.AirdropLexicon)
	}
	if isOverlong(in.Symbol, in.Name) {
		add(SignalOverlong, w.Overlong)
	}
	if in.ProviderSpam != nil && *in.ProviderSpam {
		add(SignalProviderSpam, w.ProviderSpam)
	}
	if in.ContractVerified != nil && !*in.ContractVerified {
		add(SignalUnverified, w.Unverified)
	}
	if in.HasPriceListing != nil && !*in.HasPriceListing {
		add(SignalNoListing, w.NoListing)
	}

	if score > 1 {
		score = 1
	}
	return Result{Score: score, Verdict: verdictFor(score, w), Signals: signals}
}

// verdictFor maps a score to a verdict. Unknown is reserved for the never-scored
// default and never returned here: a run that fired no signals is a positive
// judgement of legitimacy, not an absence of one.
func verdictFor(score float64, w Weights) Verdict {
	switch {
	case score >= w.ScamThreshold:
		return VerdictScam
	case score >= w.SuspectThreshold:
		return VerdictSuspect
	default:
		return VerdictLegit
	}
}

// hasInvisibleRune reports a formatting or control rune that has no business in
// a ticker or name. Ordinary spaces are allowed; everything else in the format
// (Cf) and control (Cc) categories — zero-width joiners, bidi overrides, the
// invisible separator U+2063 seen in the catalogue — is a smuggling vector.
func hasInvisibleRune(s string) bool {
	for _, r := range s {
		if r == ' ' {
			continue
		}
		if unicode.Is(unicode.Cf, r) || unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// hasMixedScript reports Latin letters sitting beside Cyrillic or Greek ones —
// the signature of a confusable clone that swaps "a", "o", "p" for identical
// Cyrillic glyphs. A name written wholly in one script is ordinary and stays.
func hasMixedScript(s string) bool {
	var latin, confusable bool
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Latin, r):
			latin = true
		case unicode.Is(unicode.Cyrillic, r), unicode.Is(unicode.Greek, r):
			confusable = true
		}
	}
	return latin && confusable
}

// domainLike matches a hostname with a plausible TLD, the signature of an
// airdrop lure that names a site to visit. Real tokens name themselves.
var domainLike = regexp.MustCompile(
	`(?i)\b[a-z0-9][a-z0-9-]*\.(com|net|org|io|xyz|app|link|site|top|vip|gift|claim|finance|fi|me|ru|cc)\b`)

func looksLikeDomain(s string) bool {
	return domainLike.MatchString(s) || strings.Contains(strings.ToLower(s), "http")
}

// airdropTerms are the vocabulary of a phishing lure written into the token's
// own name to bait an approve. A real asset does not instruct its holder.
var airdropTerms = []string{
	"visit", "claim", "reward", "bonus", "airdrop", "voucher", "giveaway",
	"redeem", "access", "$",
}

func hasAirdropLexicon(s string) bool {
	l := strings.ToLower(s)
	for _, t := range airdropTerms {
		if strings.Contains(l, t) {
			return true
		}
	}
	return false
}

// isOverlong flags a symbol or name too long to be a real ticker or project
// name — lures pack a whole sentence into the field. The bounds are generous;
// this is a weak corroborating signal, not a verdict on its own.
func isOverlong(symbol, name string) bool {
	return len([]rune(symbol)) > 12 || len([]rune(name)) > 40
}
