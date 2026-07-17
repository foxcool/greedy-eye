package entity

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAccountValidateCapabilities(t *testing.T) {
	tests := []struct {
		name    string
		account Account
		wantErr string
	}{
		{
			name:    "no capabilities is valid",
			account: Account{Type: AccountTypeWallet},
		},
		{
			name: "wallet with portfolio_sync",
			account: Account{
				Type:         AccountTypeWallet,
				Capabilities: []AccountCapability{CapabilityPortfolioSync},
			},
		},
		{
			name: "exchange with full personal set",
			account: Account{
				Type:         AccountTypeExchange,
				Capabilities: []AccountCapability{CapabilityPortfolioSync, CapabilityTrading, CapabilityMarketData},
			},
		},
		{
			name: "service key shared system-wide",
			account: Account{
				Type:         AccountTypeService,
				Capabilities: []AccountCapability{CapabilityMarketData, CapabilityOnchainLookup},
				SystemScopes: []AccountCapability{CapabilityMarketData, CapabilityOnchainLookup},
			},
		},
		{
			name: "exchange market_data shared system-wide",
			account: Account{
				Type:         AccountTypeExchange,
				Capabilities: []AccountCapability{CapabilityTrading, CapabilityMarketData},
				SystemScopes: []AccountCapability{CapabilityMarketData},
			},
		},
		{
			name: "wallet cannot trade",
			account: Account{
				Type:         AccountTypeWallet,
				Capabilities: []AccountCapability{CapabilityTrading},
			},
			wantErr: `capability "trading" is not allowed for account type "wallet"`,
		},
		{
			name: "service cannot sync portfolios",
			account: Account{
				Type:         AccountTypeService,
				Capabilities: []AccountCapability{CapabilityPortfolioSync},
			},
			wantErr: `capability "portfolio_sync" is not allowed for account type "service"`,
		},
		{
			name: "system scope must be a granted capability",
			account: Account{
				Type:         AccountTypeService,
				Capabilities: []AccountCapability{CapabilityOnchainLookup},
				SystemScopes: []AccountCapability{CapabilityMarketData},
			},
			wantErr: `system scope "market_data" is not among account capabilities`,
		},
		{
			name: "trading is never system-scoped",
			account: Account{
				Type:         AccountTypeExchange,
				Capabilities: []AccountCapability{CapabilityTrading},
				SystemScopes: []AccountCapability{CapabilityTrading},
			},
			wantErr: `capability "trading" cannot be system-scoped`,
		},
		{
			name: "manual account with manual_positions",
			account: Account{
				Type:         AccountTypeManual,
				Capabilities: []AccountCapability{CapabilityManualPositions},
			},
		},
		{
			name: "manual account cannot sync",
			account: Account{
				Type:         AccountTypeManual,
				Capabilities: []AccountCapability{CapabilityPortfolioSync},
			},
			wantErr: `capability "portfolio_sync" is not allowed for account type "manual"`,
		},
		{
			name: "manual_positions is never system-scoped",
			account: Account{
				Type:         AccountTypeManual,
				Capabilities: []AccountCapability{CapabilityManualPositions},
				SystemScopes: []AccountCapability{CapabilityManualPositions},
			},
			wantErr: `capability "manual_positions" cannot be system-scoped`,
		},
		{
			name: "wallet cannot hold manual_positions",
			account: Account{
				Type:         AccountTypeWallet,
				Capabilities: []AccountCapability{CapabilityManualPositions},
			},
			wantErr: `capability "manual_positions" is not allowed for account type "wallet"`,
		},
		{
			name: "unspecified type allows nothing",
			account: Account{
				Type:         AccountTypeUnspecified,
				Capabilities: []AccountCapability{CapabilityMarketData},
			},
			wantErr: `capability "market_data" is not allowed for account type "unspecified"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.account.ValidateCapabilities()
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, tt.wantErr)
			}
		})
	}
}
