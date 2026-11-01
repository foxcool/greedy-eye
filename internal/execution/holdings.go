package execution

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/shopspring/decimal"
)

// HoldingLister is the slice of the portfolio client that reads an account.
type HoldingLister interface {
	ListHoldings(context.Context, *connect.Request[apiv1.ListHoldingsRequest]) (*connect.Response[apiv1.ListHoldingsResponse], error)
}

// AccountHoldings reads every holding of an account, all pages, in asset units.
func AccountHoldings(ctx context.Context, client HoldingLister, accountID string) ([]Holding, error) {
	var out []Holding
	var token string
	for {
		req := &apiv1.ListHoldingsRequest{AccountId: &accountID}
		if token != "" {
			req.PageToken = &token
		}
		resp, err := client.ListHoldings(ctx, connect.NewRequest(req))
		if err != nil {
			return nil, fmt.Errorf("list holdings of account %s: %w", accountID, err)
		}
		for _, h := range resp.Msg.GetHoldings() {
			raw, err := decimal.NewFromString(h.GetAmount())
			if err != nil {
				return nil, fmt.Errorf("holding %s: invalid amount %q: %w", h.GetId(), h.GetAmount(), err)
			}
			out = append(out, Holding{
				ID:       h.GetId(),
				AssetID:  h.GetAssetId(),
				Amount:   raw.Shift(-entity.DecI32(h.GetDecimals())),
				Decimals: h.GetDecimals(),
			})
		}
		token = resp.Msg.GetNextPageToken()
		if token == "" {
			return out, nil
		}
	}
}
