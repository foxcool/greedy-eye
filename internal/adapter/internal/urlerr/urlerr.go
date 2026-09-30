// Package urlerr strips the request URL out of a transport error, for the
// adapters whose credential travels in the URL itself — a path segment
// (Alchemy) or a query parameter (Blockchair, Helius).
//
// http.Client.Do returns *url.Error, whose Error() quotes the full URL: the
// standard library redacts a userinfo password and nothing else. Wrapped with
// %w, a timeout or a reset connection therefore carried the key into the sync
// response, the sweep log line and — once chain failures were persisted — the
// database (personal-isy9 review). Keeping the operation and the cause is
// everything a reader needs to tell a timeout from a refused connection.
package urlerr

import (
	"errors"
	"fmt"
	"net/url"
)

// Strip returns err without the URL of any *url.Error in it. Other errors pass
// through unchanged; a nil error stays nil.
func Strip(err error) error {
	var ue *url.Error
	if !errors.As(err, &ue) {
		return err
	}
	return fmt.Errorf("%s request: %w", ue.Op, ue.Err)
}
