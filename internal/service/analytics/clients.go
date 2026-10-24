package analytics

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	apiv1 "github.com/foxcool/greedy-eye/api/v1"
	"github.com/foxcool/greedy-eye/internal/entity"
	"github.com/shopspring/decimal"
)

// MarketDataClient is the subset of MarketDataServiceClient that Analytics needs.
// Satisfied by *marketdata.Handler (monolith) and apiv1connect.MarketDataServiceClient
// (microservice), same as portfolio.MarketDataClient.
type MarketDataClient interface {
	GetAsset(context.Context, *connect.Request[apiv1.GetAssetRequest]) (*connect.Response[apiv1.Asset], error)
	GetLatestPrice(context.Context, *connect.Request[apiv1.GetLatestPriceRequest]) (*connect.Response[apiv1.Price], error)
	ListPriceHistory(context.Context, *connect.Request[apiv1.ListPriceHistoryRequest]) (*connect.Response[apiv1.ListPriceHistoryResponse], error)
	GetPricingStatus(context.Context, *connect.Request[apiv1.GetPricingStatusRequest]) (*connect.Response[apiv1.GetPricingStatusResponse], error)
}

// PortfolioClient is the subset of PortfolioServiceClient that Analytics needs.
// Satisfied by *portfolio.Handler (monolith) and apiv1connect.PortfolioServiceClient
// (microservice). Ownership is enforced by the portfolio service, not here.
type PortfolioClient interface {
	GetPortfolio(context.Context, *connect.Request[apiv1.GetPortfolioRequest]) (*connect.Response[apiv1.Portfolio], error)
	GetAccount(context.Context, *connect.Request[apiv1.GetAccountRequest]) (*connect.Response[apiv1.Account], error)
	ListHoldings(context.Context, *connect.Request[apiv1.ListHoldingsRequest]) (*connect.Response[apiv1.ListHoldingsResponse], error)
}

// holdingFromProto maps a holding read back from the portfolio service,
// keeping the server-stamped provenance that entity.Holding.Swept reads.
func holdingFromProto(h *apiv1.Holding) (*entity.Holding, error) {
	amount, err := decimal.NewFromString(h.Amount)
	if err != nil {
		return nil, fmt.Errorf("holding %s: invalid amount %q: %w", h.Id, h.Amount, err)
	}
	result := &entity.Holding{
		ID:          h.Id,
		Amount:      amount,
		Decimals:    h.Decimals,
		AssetID:     h.AssetId,
		AccountID:   h.AccountId,
		PortfolioID: h.GetPortfolioId(),
		Chain:       h.Chain,
		Liquidity:   entity.Liquidity(h.Liquidity),
		Excluded:    h.Excluded,
		Source:      provenanceFromProto(h.Source),
		UpdatedAt:   h.UpdatedAt.AsTime(),
	}
	if h.SyncedAt != nil {
		t := h.SyncedAt.AsTime()
		result.SyncedAt = &t
	}
	return result, nil
}

func provenanceFromProto(s apiv1.ProvenanceSource) entity.ProvenanceSource {
	switch s {
	case apiv1.ProvenanceSource_PROVENANCE_SOURCE_SYNC:
		return entity.SourceSync
	case apiv1.ProvenanceSource_PROVENANCE_SOURCE_MANUAL:
		return entity.SourceManual
	case apiv1.ProvenanceSource_PROVENANCE_SOURCE_LLM_IMPORT:
		return entity.SourceLLMImport
	default:
		return ""
	}
}
