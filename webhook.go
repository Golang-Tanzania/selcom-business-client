package business

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// Callback is the payload Selcom POSTs to the callback URL configured in the
// portal when an API-initiated transaction completes successfully. Only
// ReferenceID and Status are always present; the other members appear only
// when selected in the callback configuration. Callbacks are sent for
// successful transactions only, so a failure must be discovered via Status.
type Callback struct {
	// ReferenceID is the transId of the original Transfer.
	ReferenceID            string `json:"reference_id"`
	Status                 string `json:"status"` // always SUCCESS
	SenderAccountName      string `json:"sender_account_name"`
	SenderAccountNumber    string `json:"sender_account_number"`
	RecipientName          string `json:"recipient_name"`
	RecipientAccountNumber string `json:"recipient_account_number"`
	Amount                 Number `json:"amount"`
	Charges                Number `json:"charges"`
	SelcomReceipt          string `json:"selcom_receipt"`
}

// maxCallbackBody bounds the callback body read.
const maxCallbackBody = 1 << 20

// ParseCallback decodes a callback request. The portal documents no
// signature on callbacks, so the payload cannot be authenticated: treat it as
// a hint and confirm with Status(ctx, cb.ReferenceID) before acting on it.
func ParseCallback(r *http.Request) (Callback, error) {
	if r.Method != http.MethodPost {
		return Callback{}, fmt.Errorf("callback: method %s, want POST", r.Method)
	}

	data, err := io.ReadAll(io.LimitReader(r.Body, maxCallbackBody+1))
	if err != nil {
		return Callback{}, fmt.Errorf("callback: reading body: %w", err)
	}

	if len(data) > maxCallbackBody {
		return Callback{}, errors.New("callback: body exceeds 1 MiB")
	}

	var cb Callback
	if err := json.Unmarshal(data, &cb); err != nil {
		return Callback{}, fmt.Errorf("callback: decoding body: %w", err)
	}

	if cb.ReferenceID == "" {
		return Callback{}, errors.New("callback: reference_id is missing")
	}

	return cb, nil
}
