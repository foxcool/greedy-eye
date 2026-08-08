package portfolio

import (
	"fmt"
	"slices"
	"strings"

	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// Fields a partial update may write, mirroring the switch arms the store
// actually implements (internal/store/postgres/portfolio.go). Anything else —
// server-stamped provenance, immutable identity, a typo — is rejected rather
// than silently dropped, so a mask that names the wrong thing cannot look like
// a successful write.
var (
	portfolioUpdatable   = []string{"name", "description", "data"}
	holdingUpdatable     = []string{"amount", "decimals", "portfolio_id", "excluded", "chain", "liquidity"}
	accountUpdatable     = []string{"name", "description", "type", "data", "capabilities", "system_scopes", "portfolio_id"}
	transactionUpdatable = []string{"status", "data"}
)

// resolveMask returns the fields a partial update may write.
//
// An absent mask used to mean "every field", which turned a request carrying
// only an id into an instruction to zero the row: the exclude toggle wiped
// amount, decimals and portfolio_id off manual holdings, and a portfolio rename
// wiped its data. A write path where omitting a field destroys data is wrong
// regardless of who calls it, so the caller now has to say what it means.
func resolveMask(mask *fieldmaskpb.FieldMask, updatable []string) ([]string, error) {
	if mask == nil || len(mask.Paths) == 0 {
		return nil, fmt.Errorf("update_mask is required and must name at least one of: %s", strings.Join(updatable, ", "))
	}
	for _, path := range mask.Paths {
		if !slices.Contains(updatable, path) {
			return nil, fmt.Errorf("field %q is not updatable; update_mask accepts: %s", path, strings.Join(updatable, ", "))
		}
	}
	return mask.Paths, nil
}
