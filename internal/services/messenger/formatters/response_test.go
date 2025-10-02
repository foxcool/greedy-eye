package formatters

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/foxcool/greedy-eye/internal/api/models"
)

func TestNewResponseFormatter(t *testing.T) {
	log := zaptest.NewLogger(t)
	formatter := NewResponseFormatter(log)

	require.NotNil(t, formatter)
	assert.NotNil(t, formatter.log)
}

func TestResponseFormatter_FormatPortfolioSummary(t *testing.T) {
	log := zaptest.NewLogger(t)
	formatter := NewResponseFormatter(log)

	data := &PortfolioData{
		ID:          "portfolio123",
		Name:        "Main Portfolio",
		TotalValue:  10000.50,
		DailyChange: 250.75,
		Currency:    "USD",
		Holdings: []*HoldingData{
			{Symbol: "BTC", Amount: 0.5, Value: 25000, Change24h: 1200, Percentage: 50},
			{Symbol: "ETH", Amount: 10, Value: 20000, Change24h: 800, Percentage: 40},
		},
	}

	result, err := formatter.FormatPortfolioSummary(data, models.ResponseFormat_RESPONSE_FORMAT_MARKDOWN)

	assert.Empty(t, result)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "FormatPortfolioSummary not implemented")
}

func TestResponseFormatter_FormatBalanceInfo(t *testing.T) {
	log := zaptest.NewLogger(t)
	formatter := NewResponseFormatter(log)

	holdings := []*HoldingData{
		{Symbol: "BTC", Amount: 0.25, Value: 12500, Change24h: 600, Percentage: 62.5},
		{Symbol: "ETH", Amount: 5, Value: 7500, Change24h: 300, Percentage: 37.5},
	}

	result, err := formatter.FormatBalanceInfo(holdings, models.ResponseFormat_RESPONSE_FORMAT_PLAIN)

	assert.Empty(t, result)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "FormatBalanceInfo not implemented")
}

func TestResponseFormatter_FormatPriceData(t *testing.T) {
	log := zaptest.NewLogger(t)
	formatter := NewResponseFormatter(log)

	result, err := formatter.FormatPriceData("BTC", 50000.0, 1250.5, models.ResponseFormat_RESPONSE_FORMAT_HTML)

	assert.Empty(t, result)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "FormatPriceData not implemented")
}

func TestResponseFormatter_FormatPerformanceReport(t *testing.T) {
	log := zaptest.NewLogger(t)
	formatter := NewResponseFormatter(log)

	data := &PerformanceData{
		TotalReturn:   15.5,
		DailyReturn:   2.3,
		WeeklyReturn:  8.7,
		MonthlyReturn: 12.1,
	}

	result, err := formatter.FormatPerformanceReport(data, models.ResponseFormat_RESPONSE_FORMAT_MARKDOWN)

	assert.Empty(t, result)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "FormatPerformanceReport not implemented")
}

func TestResponseFormatter_FormatTransactionHistory(t *testing.T) {
	log := zaptest.NewLogger(t)
	formatter := NewResponseFormatter(log)

	transactions := []*TransactionData{
		{
			ID:        "tx123",
			Type:      "buy",
			Symbol:    "BTC",
			Amount:    0.1,
			Price:     49000,
			Fee:       10,
			Timestamp: 1640995200,
			Exchange:  "binance",
		},
		{
			ID:        "tx456",
			Type:      "sell",
			Symbol:    "ETH",
			Amount:    2,
			Price:     3800,
			Fee:       5,
			Timestamp: 1640908800,
			Exchange:  "coinbase",
		},
	}

	result, err := formatter.FormatTransactionHistory(transactions, models.ResponseFormat_RESPONSE_FORMAT_PLAIN)

	assert.Empty(t, result)
	assert.Error(t, err)
	
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "FormatTransactionHistory not implemented")
}

func TestResponseFormatter_FormatErrorMessage(t *testing.T) {
	log := zaptest.NewLogger(t)
	formatter := NewResponseFormatter(log)

	testCases := []struct {
		name     string
		err      error
		userLang string
		expected string
	}{
		{
			name:     "Russian error message",
			err:      errors.New("test error"),
			userLang: "ru",
			expected: "Произошла ошибка. Попробуйте позже.",
		},
		{
			name:     "English error message",
			err:      errors.New("test error"),
			userLang: "en",
			expected: "An error occurred. Please try again later.",
		},
		{
			name:     "Default English for unknown language",
			err:      errors.New("test error"),
			userLang: "fr",
			expected: "An error occurred. Please try again later.",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := formatter.FormatErrorMessage(tc.err, tc.userLang)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestResponseFormatter_FormatHelpMessage(t *testing.T) {
	log := zaptest.NewLogger(t)
	formatter := NewResponseFormatter(log)

	commands := []string{"/start", "/help", "/portfolio", "/balance"}

	testCases := []struct {
		name     string
		userLang string
		expected string
	}{
		{
			name:     "Russian help message",
			userLang: "ru",
			expected: "Доступные команды: /start, /help, /portfolio, /balance",
		},
		{
			name:     "English help message",
			userLang: "en",
			expected: "Available commands: /start, /help, /portfolio, /balance",
		},
		{
			name:     "Default English for unknown language",
			userLang: "de",
			expected: "Available commands: /start, /help, /portfolio, /balance",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := formatter.FormatHelpMessage(commands, tc.userLang)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestPortfolioData(t *testing.T) {
	data := &PortfolioData{
		ID:          "test123",
		Name:        "Test Portfolio",
		TotalValue:  5000,
		DailyChange: -150,
		Currency:    "EUR",
		Holdings: []*HoldingData{
			{Symbol: "ADA", Amount: 1000, Value: 500, Change24h: -25, Percentage: 10},
		},
		Performance: &PerformanceData{
			TotalReturn: -3.0,
		},
	}

	assert.Equal(t, "test123", data.ID)
	assert.Equal(t, "Test Portfolio", data.Name)
	assert.Equal(t, 5000.0, data.TotalValue)
	assert.Equal(t, -150.0, data.DailyChange)
	assert.Equal(t, "EUR", data.Currency)
	assert.Len(t, data.Holdings, 1)
	assert.Equal(t, "ADA", data.Holdings[0].Symbol)
	assert.NotNil(t, data.Performance)
	assert.Equal(t, -3.0, data.Performance.TotalReturn)
}

func TestTransactionData(t *testing.T) {
	tx := &TransactionData{
		ID:        "tx789",
		Type:      "trade",
		Symbol:    "DOT",
		Amount:    50,
		Price:     25.5,
		Fee:       2.5,
		Timestamp: 1641081600,
		Exchange:  "kraken",
	}

	assert.Equal(t, "tx789", tx.ID)
	assert.Equal(t, "trade", tx.Type)
	assert.Equal(t, "DOT", tx.Symbol)
	assert.Equal(t, 50.0, tx.Amount)
	assert.Equal(t, 25.5, tx.Price)
	assert.Equal(t, 2.5, tx.Fee)
	assert.Equal(t, int64(1641081600), tx.Timestamp)
	assert.Equal(t, "kraken", tx.Exchange)
}