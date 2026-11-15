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
//
// The trade key is its idempotency key: a Submit repeating a key whose fill
// was written, or may have been, answers as that first Submit did and writes
// nothing. A key that was rejected or whose import was refused wrote nothing,
// so a repeat decides afresh.
// shortcut: keys are remembered in memory, for the life of the process and
// without eviction; a retry after a restart would fill again. Upgrade when
// anything retries Submit: keep the key with the fill in the database.
type PaperVenue struct {
	client PaperPortfolio

	mu       sync.Mutex
	accounts map[string]*sync.Mutex
	placed   map[Key]placedFill
}

// placedFill is the answer Submit gave to a key whose import was sent.
type placedFill struct {
	outcome Outcome
	err     error
}

func NewPaperVenue(client PaperPortfolio) *PaperVenue {
	return &PaperVenue{client: client, accounts: map[string]*sync.Mutex{}, placed: map[Key]placedFill{}}
}

func (v *PaperVenue) placedAnswer(key Key) (placedFill, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	p, ok := v.placed[key]
	return p, ok
}

func (v *PaperVenue) remember(key Key, outcome Outcome, err error) (Outcome, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.placed[key] = placedFill{outcome: outcome, err: err}
	return outcome, err
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
	// Checked under the account lock, so a repeat waits for the first
	// Submit of its key to finish and then sees its answer.
	if p, ok := v.placedAnswer(key); ok {
		return p.outcome, p.err
	}

	res, err := v.fill(ctx, account.ID, trade)
	if err != nil {
		return Outcome{}, err
	}
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
		return v.remember(key, Outcome{}, fmt.Errorf("paper fill may be applied, check holdings of account %s: %w", account.ID, err))
	}
	if failed := failedItems(resp.Msg); failed != "" {
		return Outcome{}, fmt.Errorf("paper fill not applied to account %s: %s", account.ID, failed)
	}

	ids := []string{res.Quote.ID}
	if res.Base.ID != "" {
		ids = append(ids, res.Base.ID)
	} else if id := v.createdHolding(ctx, account.ID, trade.AssetID); id != "" {
		ids = append(ids, id)
	}
	return v.remember(key, Outcome{Status: OutcomeFilled, Fill: res.Fill, HoldingIDs: ids}, nil)
}

// Preview decides the trade as Submit does and writes nothing.
func (v *PaperVenue) Preview(ctx context.Context, account Account, trade Trade) (Outcome, error) {
	res, err := v.fill(ctx, account.ID, trade)
	if err != nil {
		return Outcome{}, err
	}
	if res.Reason != "" {
		return Outcome{Status: OutcomeRejected, Reason: res.Reason}, nil
	}
	return Outcome{Status: OutcomeFilled, Fill: res.Fill}, nil
}

// fill decides a trade against the account's current holdings.
func (v *PaperVenue) fill(ctx context.Context, accountID string, trade Trade) (paperFillResult, error) {
	holdings, err := AccountHoldings(ctx, v.client, accountID)
	if err != nil {
		return paperFillResult{}, err
	}
	return paperFill(trade, holdings), nil
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

// failedItems names the positions an import refused. The import commits a
// batch all or none, so any failed item means the batch was not applied.
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
