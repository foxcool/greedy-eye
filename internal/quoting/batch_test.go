package quoting

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/stretchr/testify/assert"
)

// A pair the caller did not collect must fail, not read as "no price": the
// latter would drop a priced asset from the valuation without a trace.
func TestPrefetched_UncollectedPairFails(t *testing.T) {
	p := Prefetched{{Asset: "eth", Base: "USD"}: nil}

	_, err := p.GetLatestPrice(context.Background(), connect.NewRequest(&apiv1.GetLatestPriceRequest{AssetId: "eth", BaseAssetId: "USD"}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "collected, no price")

	_, err = p.GetLatestPrice(context.Background(), connect.NewRequest(&apiv1.GetLatestPriceRequest{AssetId: "eth"}))
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err), "never collected")
}
