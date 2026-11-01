package execution

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pagedHoldings serves one holding per page and checks the token it is given.
type pagedHoldings struct {
	tokens []string
}

func (p *pagedHoldings) ListHoldings(_ context.Context, req *connect.Request[apiv1.ListHoldingsRequest]) (*connect.Response[apiv1.ListHoldingsResponse], error) {
	p.tokens = append(p.tokens, req.Msg.GetPageToken())
	if req.Msg.PageToken != nil && *req.Msg.PageToken == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, assert.AnError)
	}
	switch req.Msg.GetPageToken() {
	case "":
		return connect.NewResponse(&apiv1.ListHoldingsResponse{
			Holdings:      []*apiv1.Holding{{Id: "h-1", AssetId: "a", Amount: "12345", Decimals: 2}},
			NextPageToken: "page-2",
		}), nil
	case "page-2":
		return connect.NewResponse(&apiv1.ListHoldingsResponse{
			Holdings: []*apiv1.Holding{{Id: "h-2", AssetId: "b", Amount: "7", Decimals: 0}},
		}), nil
	}
	return nil, connect.NewError(connect.CodeInvalidArgument, assert.AnError)
}

func TestAccountHoldings_ReadsEveryPageInUnits(t *testing.T) {
	p := &pagedHoldings{}
	got, err := AccountHoldings(context.Background(), p, "acct")
	require.NoError(t, err)
	assert.Equal(t, []string{"", "page-2"}, p.tokens)
	require.Len(t, got, 2)
	assert.Equal(t, "123.45", got[0].Amount.String())
	assert.Equal(t, uint32(2), got[0].Decimals)
	assert.Equal(t, "7", got[1].Amount.String())
}
