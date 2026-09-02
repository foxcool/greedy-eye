package portfolio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	tinvestadapter "github.com/foxcool/greedy-eye/internal/adapter/tinvest"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// The one place in this package that names an adapter, and only in a test.
//
// The seam this ticket adds is exactly adapter -> service: an instrument type
// and a venue leave the adapter and become an asset's identity, and every other
// test here replaces the adapter with a mock that agrees with the handler by
// construction. Two shapes that agree in the mocks and disagree over the wire
// is the class of bug that costs a release, and it is the class this smoke
// exists to catch. No production file in the package imports an adapter.
//
// The fixtures are the synthetic capture from internal/adapter/tinvest/testdata
// — real field names, invented numbers — served over an http replay host, which
// is what account.data["base_url"] was built for (personal-hbb1).

func replayServer(t *testing.T) *httptest.Server {
	t.Helper()
	fixture := func(name string) []byte {
		raw, err := os.ReadFile(filepath.Join("..", "..", "adapter", "tinvest", "testdata", name))
		require.NoError(t, err)
		return raw
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case pathHasSuffix(r.URL.Path, "OperationsService/GetPortfolio"):
			_, _ = w.Write(fixture("portfolio_shapes.json"))
		case pathHasSuffix(r.URL.Path, "InstrumentsService/Bonds"):
			_, _ = w.Write(fixture("bonds_shapes.json"))
		default:
			// Shares and Etfs: an empty universe, which is the case worth
			// exercising here — the catalogue does not carry the position, so
			// the venue comes from the board the broker reported it on and the
			// guess is counted.
			_, _ = w.Write([]byte(`{"instruments":[]}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func pathHasSuffix(path, suffix string) bool {
	return len(path) >= len(suffix) && path[len(path)-len(suffix):] == suffix
}

// TestSyncAccount_BrokerEndToEndOverTheWire drives the real adapter, over HTTP,
// into the real write path: what the broker sends becomes holdings, each under
// the identity its own line implies.
func TestSyncAccount_BrokerEndToEndOverTheWire(t *testing.T) {
	srv := replayServer(t)

	// Transport is supplied the way the registry supplies it (there, the rate
	// limiter's): a client given one owns its own trust decisions, which is what
	// lets a plaintext replay host need no anchor.
	client, err := tinvestadapter.NewClient(tinvestadapter.Config{
		Token: "t", BaseURL: srv.URL, Transport: http.DefaultTransport,
	})
	require.NoError(t, err)
	syncer := tinvestadapter.NewBrokerSyncer(client, "2000123456")

	acct := brokerAccount()
	s := &mockStore{}
	s.On("GetAccount", mock.Anything, testAccountID).Return(acct, nil)
	s.On("ListHoldings", mock.Anything, mock.Anything).Return([]*entity.Holding{}, "", nil)
	var written []*entity.Holding
	s.On("CreateHolding", mock.Anything, mock.Anything).Return(&entity.Holding{ID: testHoldingID}, nil).
		Run(func(args mock.Arguments) {
			written = append(written, args.Get(1).(*entity.Holding))
		})

	md := &recordingMD{mockMDClient: mockMDClient{autoAsset: true}}
	md.On("FetchExternalPrices", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&apiv1.FetchExternalPricesResponse{}), nil)

	h := newHandler(s).WithMarketDataClient(md).WithBrokerSyncerSource(&mockBrokerSource{syncer: syncer})

	resp, err := h.SyncAccount(ctxWithUser(testUserID), connect.NewRequest(&apiv1.SyncAccountRequest{AccountId: testAccountID}))
	require.NoError(t, err)
	require.Empty(t, resp.Msg.Errors)

	// Ten lines in, ten holdings out: nothing was dropped on the way.
	assert.Equal(t, int32(10), resp.Msg.HoldingsUpserted)
	assert.Equal(t, int32(0), resp.Msg.PositionsSkipped)
	require.Len(t, written, 10)

	byType := map[apiv1.AssetType]int{}
	for _, req := range md.assetRequests {
		byType[req.Type]++
		require.NotEmpty(t, req.GetMarket(), "%s reached the catalogue with no venue", req.Symbol)
		switch req.Type {
		case apiv1.AssetType_ASSET_TYPE_FOREX:
			// Cash is identified by its code, never by the broker's id for the
			// settlement instrument it trades through.
			assert.Empty(t, req.GetExternalRef(), "cash line %s carries a ref", req.Symbol)
			assert.Equal(t, "forex", req.GetMarket())
		default:
			assert.Equal(t, tinvestadapter.RefSource, req.GetExternalRefSource(), "%s", req.Symbol)
			assert.NotEmpty(t, req.GetExternalRef(), "%s reached the catalogue unidentified", req.Symbol)
			assert.Contains(t, []string{"moex", "spbex"}, req.GetMarket(), "%s", req.Symbol)
		}
	}
	// The capture's own composition: 4 shares, 2 funds, 1 bond, 3 cash lines.
	assert.Equal(t, 4, byType[apiv1.AssetType_ASSET_TYPE_STOCK])
	assert.Equal(t, 2, byType[apiv1.AssetType_ASSET_TYPE_FUND])
	assert.Equal(t, 1, byType[apiv1.AssetType_ASSET_TYPE_BOND])
	assert.Equal(t, 3, byType[apiv1.AssetType_ASSET_TYPE_FOREX])

	// The bond is the one instrument the served catalogue carries, so its venue
	// is read rather than inferred: everything else is a counted guess.
	assert.Equal(t, int32(6), resp.Msg.AssetsDefaultedMarket,
		"shares and funds are absent from the served universe; the bond and the cash lines are not guesses")
}

// recordingMD keeps the FindOrCreateAsset requests so a test can assert on the
// identity each position was resolved under, which is what this seam is about.
type recordingMD struct {
	mockMDClient
	assetRequests []*apiv1.FindOrCreateAssetRequest
}

func (m *recordingMD) FindOrCreateAsset(ctx context.Context, req *connect.Request[apiv1.FindOrCreateAssetRequest]) (*connect.Response[apiv1.FindOrCreateAssetResponse], error) {
	m.assetRequests = append(m.assetRequests, req.Msg)
	return m.mockMDClient.FindOrCreateAsset(ctx, req)
}
