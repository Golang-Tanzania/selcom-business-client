package business

import "context"

type balanceInput struct {
	AccountNumber string `json:"account_number"`
}

// Balance returns the available balance of an organisation account linked
// to the API credentials (POST /v1/balance).
func (c *Client) Balance(ctx context.Context, accountNumber string) (BalanceResponse, error) {
	if accountNumber == "" {
		return BalanceResponse{}, invalidInput("balance", "account_number is required")
	}

	var resp BalanceResponse
	if err := c.post(ctx, "/balance", balanceInput{AccountNumber: accountNumber}, &resp); err != nil {
		return BalanceResponse{}, err
	}

	return resp, nil
}
