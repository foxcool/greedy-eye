package storage

import (
	"errors"
	"fmt"

	"github.com/foxcool/greedy-eye/internal/api/models"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent/account"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent/asset"
	"github.com/foxcool/greedy-eye/internal/services/storage/ent/transaction"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func entUserToProtoUser(entUser *ent.User) (*models.User, error) {
	if entUser == nil {
		return nil, errors.New("user is nil")
	}

	return &models.User{
		Id:          entUser.UUID.String(),
		Email:       entUser.Email,
		Name:        entUser.Name,
		Preferences: entUser.Preferences,
		CreatedAt:   timestamppb.New(entUser.CreatedAt),
		UpdatedAt:   timestamppb.New(entUser.UpdatedAt),
	}, nil
}

func entAssetToProtoAsset(entAsset *ent.Asset) (*models.Asset, error) {
	if entAsset == nil {
		return nil, errors.New("asset is nil")
	}

	protoType, err := entAssetTypeToProto(entAsset.Type)
	if err != nil {
		return nil, fmt.Errorf("failed to convert asset type: %w", err)
	}

	return &models.Asset{
		Id:        entAsset.UUID.String(),
		Symbol:    &entAsset.Symbol,
		Name:      entAsset.Name,
		Type:      protoType,
		Tags:      entAsset.Tags,
		CreatedAt: timestamppb.New(entAsset.CreatedAt),
		UpdatedAt: timestamppb.New(entAsset.UpdatedAt),
	}, nil
}

func protoAssetTypeToEnt(pt models.AssetType) (asset.Type, error) {
	switch pt {
	case models.AssetType_ASSET_TYPE_UNSPECIFIED:
		return asset.TypeUnspecified, nil
	case models.AssetType_ASSET_TYPE_CRYPTOCURRENCY:
		return asset.TypeCryptocurrency, nil
	case models.AssetType_ASSET_TYPE_STOCK:
		return asset.TypeStock, nil
	case models.AssetType_ASSET_TYPE_BOND:
		return asset.TypeBond, nil
	case models.AssetType_ASSET_TYPE_COMMODITY:
		return asset.TypeCommodity, nil
	case models.AssetType_ASSET_TYPE_FOREX:
		return asset.TypeForex, nil
	case models.AssetType_ASSET_TYPE_FUND:
		return asset.TypeFund, nil
	default:
		return "", fmt.Errorf("unknown asset type: %v", pt)
	}
}

func entAssetTypeToProto(pt asset.Type) (models.AssetType, error) {
	switch pt {
	case asset.TypeUnspecified:
		return models.AssetType_ASSET_TYPE_UNSPECIFIED, nil
	case asset.TypeCryptocurrency:
		return models.AssetType_ASSET_TYPE_CRYPTOCURRENCY, nil
	case asset.TypeStock:
		return models.AssetType_ASSET_TYPE_STOCK, nil
	case asset.TypeBond:
		return models.AssetType_ASSET_TYPE_BOND, nil
	case asset.TypeCommodity:
		return models.AssetType_ASSET_TYPE_COMMODITY, nil
	case asset.TypeForex:
		return models.AssetType_ASSET_TYPE_FOREX, nil
	case asset.TypeFund:
		return models.AssetType_ASSET_TYPE_FUND, nil
	default:
		return models.AssetType_ASSET_TYPE_UNSPECIFIED, fmt.Errorf("unknown asset type: %v", pt)
	}
}

func stringToUUID(id string) (uuid.UUID, error) {
	if id == "" {
		return uuid.Nil, errors.New("ID is empty")
	}
	parsedUUID, err := uuid.Parse(id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid UUID format: %w", err)
	}
	return parsedUUID, nil
}

func entAccountToProtoAccount(entAccount *ent.Account) (*models.Account, error) {
	if entAccount == nil {
		return nil, errors.New("account is nil")
	}

	if entAccount.Edges.User == nil {
		return nil, errors.New("user edge is nil: use .WithUser() eager")
	}

	protoType, err := entAccountTypeToProto(entAccount.Type)
	if err != nil {
		return nil, fmt.Errorf("failed to convert account type: %w", err)
	}

	// Handle optional description
	var desc *string
	if entAccount.Description != "" {
		desc = &entAccount.Description
	}

	return &models.Account{
		Id:          entAccount.UUID.String(),
		UserId:      entAccount.Edges.User.UUID.String(),
		Name:        entAccount.Name,
		Description: desc,
		Type:        protoType,
		Data:        entAccount.Data,
		CreatedAt:   timestamppb.New(entAccount.CreatedAt),
		UpdatedAt:   timestamppb.New(entAccount.UpdatedAt),
	}, nil
}

func protoAccountTypeToEnt(pt models.AccountType) (account.Type, error) {
	switch pt {
	case models.AccountType_ACCOUNT_TYPE_UNSPECIFIED:
		return account.TypeUnspecified, nil
	case models.AccountType_ACCOUNT_TYPE_WALLET:
		return account.TypeWallet, nil
	case models.AccountType_ACCOUNT_TYPE_EXCHANGE:
		return account.TypeExchange, nil
	case models.AccountType_ACCOUNT_TYPE_BANK:
		return account.TypeBank, nil
	case models.AccountType_ACCOUNT_TYPE_BROKER:
		return account.TypeBroker, nil
	default:
		return "", fmt.Errorf("unknown account type: %v", pt)
	}
}

func entAccountTypeToProto(pt account.Type) (models.AccountType, error) {
	switch pt {
	case account.TypeUnspecified:
		return models.AccountType_ACCOUNT_TYPE_UNSPECIFIED, nil
	case account.TypeWallet:
		return models.AccountType_ACCOUNT_TYPE_WALLET, nil
	case account.TypeExchange:
		return models.AccountType_ACCOUNT_TYPE_EXCHANGE, nil
	case account.TypeBank:
		return models.AccountType_ACCOUNT_TYPE_BANK, nil
	case account.TypeBroker:
		return models.AccountType_ACCOUNT_TYPE_BROKER, nil
	default:
		return models.AccountType_ACCOUNT_TYPE_UNSPECIFIED, fmt.Errorf("unknown account type: %v", pt)
	}
}

func entHoldingToProtoHolding(entHolding *ent.Holding) (*models.Holding, error) {
	if entHolding == nil {
		return nil, errors.New("holding is nil")
	}
	if entHolding.Edges.Asset == nil {
		return nil, errors.New("asset edge is nil: use .WithAsset() eager")
	}
	if entHolding.Edges.Account == nil {
		return nil, errors.New("account edge are nil: use .WithAccount() eager")
	}

	holding := &models.Holding{
		Id:        entHolding.UUID.String(),
		CreatedAt: timestamppb.New(entHolding.CreatedAt),
		UpdatedAt: timestamppb.New(entHolding.UpdatedAt),
		Amount:    entHolding.Amount,
		Decimals:  uint32(entHolding.Decimals),
		AssetId:   entHolding.Edges.Asset.UUID.String(),
		AccountId: entHolding.Edges.Account.UUID.String(),
	}

	if entHolding.Edges.Portfolio != nil {
		portfolioUUID := entHolding.Edges.Portfolio.UUID.String()
		holding.PortfolioId = &portfolioUUID
	}

	return holding, nil
}

func entPortfolioToProtoPortfolio(entPortfolio *ent.Portfolio) (*models.Portfolio, error) {
	if entPortfolio == nil {
		return nil, errors.New("portfolio is nil")
	}
	if entPortfolio.Edges.User == nil {
		return nil, errors.New("user edge is nil: use .WithUser() eager")
	}
	var desc *string
	if entPortfolio.Description != "" {
		desc = &entPortfolio.Description
	}
	return &models.Portfolio{
		Id:          entPortfolio.UUID.String(),
		UserId:      entPortfolio.Edges.User.UUID.String(),
		Name:        entPortfolio.Name,
		Description: desc,
		CreatedAt:   timestamppb.New(entPortfolio.CreatedAt),
		UpdatedAt:   timestamppb.New(entPortfolio.UpdatedAt),
	}, nil
}

func entPriceToProtoPrice(entPrice *ent.Price) (*models.Price, error) {
	if entPrice == nil {
		return nil, errors.New("price is nil")
	}
	if entPrice.Edges.Asset == nil {
		return nil, errors.New("asset edge is nil: use .WithAsset() eager")
	}
	if entPrice.Edges.BaseAsset == nil {
		return nil, errors.New("base asset edge is nil: use .WithBaseAsset() eager")
	}

	// Maybe fill OHLCV if non-zero
	var open, high, low, close, volume *int64
	if entPrice.Open != 0 {
		open = &entPrice.Open
	}
	if entPrice.High != 0 {
		high = &entPrice.High
	}
	if entPrice.Low != 0 {
		low = &entPrice.Low
	}
	if entPrice.Close != 0 {
		close = &entPrice.Close
	}
	if entPrice.Volume != 0 {
		volume = &entPrice.Volume
	}

	return &models.Price{
		Id:          entPrice.UUID.String(),
		SourceId:    entPrice.SourceID,
		AssetId:     entPrice.Edges.Asset.UUID.String(),
		BaseAssetId: entPrice.Edges.BaseAsset.UUID.String(),
		Interval:    entPrice.Interval,
		Decimals:    entPrice.Decimals,
		Last:        entPrice.Last,
		Open:        open,
		High:        high,
		Low:         low,
		Close:       close,
		Volume:      volume,
		Timestamp:   timestamppb.New(entPrice.Timestamp),
	}, nil
}

func entTransactionToProtoTransaction(entTx *ent.Transaction) (*models.Transaction, error) {
	if entTx == nil {
		return nil, errors.New("transaction is nil")
	}

	if entTx.Edges.Account == nil {
		return nil, errors.New("account edge are nil: use .WithAccount() eager")
	}

	protoType, protoTypeErr := entTransactionTypeToProto(entTx.Type)
	protoStatus, protoStatusErr := entTransactionStatusToProto(entTx.Status)
	if protoTypeErr != nil {
		return nil, protoTypeErr
	}
	if protoStatusErr != nil {
		return nil, protoStatusErr
	}

	return &models.Transaction{
		Id:        entTx.UUID.String(),
		Type:      protoType,
		Status:    protoStatus,
		Data:      entTx.Data,
		AccountId: entTx.Edges.Account.UUID.String(),
		CreatedAt: timestamppb.New(entTx.CreatedAt),
		UpdatedAt: timestamppb.New(entTx.UpdatedAt),
	}, nil
}

func entTransactionTypeToProto(entType transaction.Type) (models.TransactionType, error) {
	switch entType {
	case transaction.TypeExtended:
		return models.TransactionType_TRANSACTION_TYPE_EXTENDED, nil
	case transaction.TypeTrade:
		return models.TransactionType_TRANSACTION_TYPE_TRADE, nil
	case transaction.TypeTransfer:
		return models.TransactionType_TRANSACTION_TYPE_TRANSFER, nil
	case transaction.TypeDeposit:
		return models.TransactionType_TRANSACTION_TYPE_DEPOSIT, nil
	case transaction.TypeWithdrawal:
		return models.TransactionType_TRANSACTION_TYPE_WITHDRAWAL, nil
	default:
		return models.TransactionType_TRANSACTION_TYPE_UNSPECIFIED, errors.New("unknown transaction type")
	}
}

func entTransactionStatusToProto(entStatus transaction.Status) (models.TransactionStatus, error) {
	switch entStatus {
	case transaction.StatusPending:
		return models.TransactionStatus_TRANSACTION_STATUS_PENDING, nil
	case transaction.StatusCompleted:
		return models.TransactionStatus_TRANSACTION_STATUS_COMPLETED, nil
	case transaction.StatusFailed:
		return models.TransactionStatus_TRANSACTION_STATUS_FAILED, nil
	case transaction.StatusCancelled:
		return models.TransactionStatus_TRANSACTION_STATUS_CANCELLED, nil
	default:
		return models.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED, errors.New("unknown transaction status")
	}
}

func protoTransactionTypeToEnt(protoType models.TransactionType) (transaction.Type, error) {
	switch protoType {
	case models.TransactionType_TRANSACTION_TYPE_EXTENDED:
		return transaction.TypeExtended, nil
	case models.TransactionType_TRANSACTION_TYPE_TRADE:
		return transaction.TypeTrade, nil
	case models.TransactionType_TRANSACTION_TYPE_TRANSFER:
		return transaction.TypeTransfer, nil
	case models.TransactionType_TRANSACTION_TYPE_DEPOSIT:
		return transaction.TypeDeposit, nil
	case models.TransactionType_TRANSACTION_TYPE_WITHDRAWAL:
		return transaction.TypeWithdrawal, nil
	default:
		return transaction.TypeUnspecified, errors.New("unknown transaction type")
	}
}

func protoTransactionStatusToEnt(protoStatus models.TransactionStatus) (transaction.Status, error) {
	switch protoStatus {
	case models.TransactionStatus_TRANSACTION_STATUS_PENDING:
		return transaction.StatusPending, nil
	case models.TransactionStatus_TRANSACTION_STATUS_COMPLETED:
		return transaction.StatusCompleted, nil
	case models.TransactionStatus_TRANSACTION_STATUS_FAILED:
		return transaction.StatusFailed, nil
	case models.TransactionStatus_TRANSACTION_STATUS_CANCELLED:
		return transaction.StatusCancelled, nil
	default:
		return transaction.StatusUnspecified, errors.New("unknown transaction status")
	}
}
