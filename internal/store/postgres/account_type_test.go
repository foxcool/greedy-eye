package postgres

import (
	"testing"

	"github.com/foxcool/greedy-eye/internal/entity"
)

// TestAccountTypeStringRoundtrip guards the DB string mapping: every account
// type written by accountTypeToString must scan back to the same type, so a
// newly added enum value cannot silently degrade to Unspecified on read.
func TestAccountTypeStringRoundtrip(t *testing.T) {
	types := []entity.AccountType{
		entity.AccountTypeWallet,
		entity.AccountTypeExchange,
		entity.AccountTypeBank,
		entity.AccountTypeBroker,
		entity.AccountTypeService,
		entity.AccountTypeManual,
	}
	for _, typ := range types {
		if got := stringToAccountType(accountTypeToString(typ)); got != typ {
			t.Errorf("roundtrip %q: got %v, want %v", accountTypeToString(typ), got, typ)
		}
	}
}
