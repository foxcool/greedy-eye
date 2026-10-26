//go:build smoke

package smoke_test

import "github.com/google/uuid"

// smokeTestUserID is the fixed identity used for all smoke test requests.
// Must be a valid UUID — UserStore.GetOrCreate validates the format.
// All data created during smoke tests lives in the compose test database
// under this well-known ID, making test rows easy to identify.
const (
	smokeTestUserID      = "00000000-0000-0000-0000-c0ffee000001"
	smokeTestOtherUserID = "00000000-0000-0000-0000-c0ffee000002"
)

// newUserID gives a test a caller of its own. A cross-service hop carries the
// caller in X-User-Id, so hops seen during a test are attributed to it by
// that id alone.
func newUserID() string {
	return uuid.NewString()
}
