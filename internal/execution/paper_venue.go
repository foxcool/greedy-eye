package execution

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
)

// PaperPortfolio is what the paper venue needs from the portfolio service:
// read the account, and write absolute positions through the import every
// other manual write uses — no RPC of its own.
type PaperPortfolio interface {
	HoldingLister
	ImportPositions(context.Context, *connect.Request[apiv1.ImportPositionsRequest]) (*connect.Response[apiv1.ImportPositionsResponse], error)
}

// PaperVenue is a simulated exchange on a manual account, and the only way an
// execution moves its holdings. Fills are written as absolute amounts under
// import_id = the execution id, so they are told apart from hand imports.
type PaperVenue struct {
	client PaperPortfolio

	mu       sync.Mutex
	accounts map[string]*sync.Mutex
}

func NewPaperVenue(client PaperPortfolio) *PaperVenue {
	return &PaperVenue{client: client, accounts: map[string]*sync.Mutex{}}
}

// accountLock serialises read-fill-write per account: the scheduler runs jobs
// concurrently, and two rules on one account would otherwise each write a
// balance computed from the same read, losing one fill.
// shortcut: lock is per process; a second scheduler instance needs an advisory lock (personal-tdey)
func (v *PaperVenue) accountLock(accountID string) *sync.Mutex {
	v.mu.Lock()
	defer v.mu.Unlock()
	l, ok := v.accounts[accountID]
	if !ok {
		l = &sync.Mutex{}
		v.accounts[accountID] = l
	}
	return l
}

func (v *PaperVenue) Submit(ctx context.Context, account Account, trade Trade, key Key) (Outcome, error) {
	lock := v.accountLock(account.ID)
	lock.Lock()
	defer lock.Unlock()

	holdings, err := AccountHoldings(ctx, v.client, account.ID)
	if err != nil {
		return Outcome{}, err
	}
	res := paperFill(trade, holdings)
	if res.Reason != "" {
		return Outcome{Status: OutcomeRejected, Reason: res.Reason}, nil
	}

	importID := key.ExecutionID
	resp, err := v.client.ImportPositions(ctx, connect.NewRequest(&apiv1.ImportPositionsRequest{
		AccountId: account.ID,
		Positions: []*apiv1.ImportPositionItem{positionItem(res.Quote), positionItem(res.Base)},
		ImportId:  &importID,
	}))
	if err != nil {
		// The call may have failed after the portfolio side wrote.
		return Outcome{}, fmt.Errorf("paper fill may be applied, check holdings of account %s: %w", account.ID, err)
	}
	if failed := failedItems(resp.Msg); failed != "" {
		return Outcome{}, fmt.Errorf("paper fill may be applied in part, check holdings of account %s: %s", account.ID, failed)
	}

	ids := []string{res.Quote.ID}
	if res.Base.ID != "" {
		ids = append(ids, res.Base.ID)
	} else if id := v.createdHolding(ctx, account.ID, trade.AssetID); id != "" {
		ids = append(ids, id)
	}
	return Outcome{Status: OutcomeFilled, Fill: res.Fill, HoldingIDs: ids}, nil
}

// createdHolding finds the holding an import has just created. The fill is
// already written, so a failed lookup costs only the id in the report, never
// the fill itself.
func (v *PaperVenue) createdHolding(ctx context.Context, accountID, assetID string) string {
	holdings, err := AccountHoldings(ctx, v.client, accountID)
	if err != nil {
		return ""
	}
	if h, found, ambiguous := (Snapshot{Holdings: holdings}).Holding(assetID); found && !ambiguous {
		return h.ID
	}
	return ""
}

// failedItems names the positions an import did not write. Items are applied
// one by one, so the others may have been.
func failedItems(resp *apiv1.ImportPositionsResponse) string {
	var failed []string
	for _, item := range resp.GetItems() {
		if item.Error != nil {
			failed = append(failed, fmt.Sprintf("%s: %s", item.GetAssetId(), item.GetError()))
		}
	}
	return strings.Join(failed, "; ")
}

func positionItem(h Holding) *apiv1.ImportPositionItem {
	assetID, decimals := h.AssetID, h.Decimals
	return &apiv1.ImportPositionItem{AssetId: &assetID, Amount: h.Amount.String(), Decimals: &decimals}
}
