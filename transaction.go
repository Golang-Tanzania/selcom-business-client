package business

import (
	"context"
	"strconv"
)

// Transfer sends money from the account linked to the API credentials to a
// bank account, mobile wallet or Selcom account (POST /v1/transaction/process).
//
// A 200 answer means the gateway accepted the request; read the Outcome.
// data.status is ACCEPTED for asynchronous rails (banks, wallets) and
// COMPLETED for internal transfers. Never resubmit while the outcome is in
// progress or ambiguous; poll Status at intervals of a few minutes until
// IsFinal reports true.
func (c *Client) Transfer(ctx context.Context, in TransferInput) (TransferResponse, error) {
	if err := in.validate(); err != nil {
		return TransferResponse{}, err
	}

	var resp TransferResponse
	if err := c.post(ctx, "/transaction/process", in, &resp); err != nil {
		return TransferResponse{}, err
	}

	return resp, nil
}

// Lookup resolves the account holder's name, and optionally the charges for
// an amount, before a transfer (GET /v1/account/lookup).
func (c *Client) Lookup(ctx context.Context, in LookupInput) (LookupResponse, error) {
	const op = "lookup"

	switch {
	case in.Bank == "":
		return LookupResponse{}, invalidInput(op, "bank is required")
	case in.Account == "":
		return LookupResponse{}, invalidInput(op, "account is required")
	case in.TransactionID == "":
		return LookupResponse{}, invalidInput(op, "transId is required")
	case in.Amount < 0:
		return LookupResponse{}, invalidInput(op, "amount must not be negative")
	}

	params := []field{
		{key: "bank", value: in.Bank},
		{key: "account", value: in.Account},
		{key: "transId", value: in.TransactionID},
	}

	if in.Amount > 0 {
		params = append(params, field{key: "amount", value: strconv.Itoa(in.Amount)})
	}

	var resp LookupResponse
	if err := c.get(ctx, "/account/lookup", params, &resp); err != nil {
		return LookupResponse{}, err
	}

	return resp, nil
}

// Status queries a previously submitted transaction by its transId
// (GET /v1/transaction/query).
func (c *Client) Status(ctx context.Context, transactionID string) (StatusResponse, error) {
	if transactionID == "" {
		return StatusResponse{}, invalidInput("status", "transId is required")
	}

	params := []field{{key: "transId", value: transactionID}}

	var resp StatusResponse
	if err := c.get(ctx, "/transaction/query", params, &resp); err != nil {
		return StatusResponse{}, err
	}

	return resp, nil
}
