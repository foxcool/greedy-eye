//go:build smoke

package smoke_test

// smokeTestUserID is the fixed identity used for all smoke test requests.
// Must be a valid UUID — UserStore.GetOrCreate validates the format.
// All data created during smoke tests lives in the compose test database
// under this well-known ID, making test rows easy to identify.
const (
	smokeTestUserID      = "00000000-0000-0000-0000-c0ffee000001"
	smokeTestOtherUserID = "00000000-0000-0000-0000-c0ffee000002"
)
