package entity

import "time"

// SkipKind says why the credentials resolver passed over an account that
// claims a capability. Each kind is a different repair for its owner.
type SkipKind string

const (
	// SkipNoAdapter: the account names a provider this build cannot talk to.
	SkipNoAdapter SkipKind = "no_adapter"
	// SkipCannotBuild: a client could not be built from the account's data.
	SkipCannotBuild SkipKind = "cannot_build"
	// SkipShadowed: another account for the same provider is chosen first.
	SkipShadowed SkipKind = "shadowed"
	// SkipOperators: unattended work will not choose between several
	// credential holders. Not about one account.
	SkipOperators SkipKind = "operators"
	// SkipDisabled: the account's owner stood it down.
	SkipDisabled SkipKind = "disabled"
)

// SkippedAccount is one account the resolver could have used and did not.
type SkippedAccount struct {
	Provider  string
	AccountID string
	Kind      SkipKind
	// Reason is a phrase for a log line. For SkipCannotBuild it carries the
	// constructor's error text, which is not safe to show to everyone.
	Reason string
}

// PriceSourceState is one price source the resolver reached for a user, and
// whether its credential can carry unattended work right now.
type PriceSourceState struct {
	Provider string
	// AccountID is the account whose credential serves the source. Empty for
	// a source that needs no credential.
	AccountID string
	Unusable  bool
	Reason    string
	// Until is when the source may be asked again. Zero when not unusable.
	Until time.Time
}

// PriceSourceReport is what one resolution of a user's price registry saw.
type PriceSourceReport struct {
	Sources []PriceSourceState
	Skipped []SkippedAccount
}
