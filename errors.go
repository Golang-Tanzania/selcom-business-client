package business

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrInvalidInput is returned (wrapped) when a request is rejected locally
// before it is sent.
var ErrInvalidInput = errors.New("client: invalid input")

func invalidInput(op, reason string) error {
	return fmt.Errorf("%w: %s: %s", ErrInvalidInput, op, reason)
}

// Gateway error codes (error_code / resultcode in error responses). The
// sandbox uses negative codes for header failures (for example -11 for a
// missing api-key) where production uses the codes below.
const (
	ErrCodeIPNotWhitelisted     = 611 // 403: requesting IP is not whitelisted for this credential.
	ErrCodeMissingHeaders       = 612 // 400: api-key, timestamp or digest header missing.
	ErrCodeInvalidAPIKey        = 613 // 401: API key invalid or deactivated (503 when the auth service is down).
	ErrCodeOriginNotRegistered  = 617 // 403: no domain registered for the key, or the Origin header does not match it.
	ErrCodeGeneral              = 621 // 400/404: generic error or resource not found.
	ErrCodeInvalidSignature     = 631 // 401: digest does not verify.
	ErrCodeStaleTimestamp       = 632 // 400: timestamp outside the ±5 minute window or malformed.
	ErrCodeAccountNotAuthorized = 641 // 400: account not found or not permitted for this operation.
	ErrCodeTransactionFailed    = 642 // 400: processing failed at the bank or wallet.
	ErrCodeDuplicate            = 643 // 422: a transaction with the same parameters was recently processed.
	ErrCodeNotOwner             = 646 // 403: transaction was not initiated by this API key.
	ErrCodeValidation           = 651 // 422: field validation failed; Data holds field errors.
)

// Error is a non-200 answer from the gateway.
type Error struct {
	HTTPStatus int
	ErrorCode  int
	Message    string
	Result     string
	ResultCode string
	// Data is the envelope's data member. For validation errors (651) it is
	// an object of field name to messages; otherwise usually an empty array.
	Data json.RawMessage
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e.ErrorCode == 0 {
		return fmt.Sprintf("selcom business: http %d: %s", e.HTTPStatus, e.Message)
	}

	return fmt.Sprintf("selcom business: http %d: error %d: %s", e.HTTPStatus, e.ErrorCode, e.Message)
}

// FieldErrors decodes the per-field messages of a validation error. It
// returns nil when the error is not a validation error.
func (e *Error) FieldErrors() map[string][]string {
	if e.ErrorCode != ErrCodeValidation || len(e.Data) == 0 {
		return nil
	}

	var m map[string][]string
	if err := json.Unmarshal(e.Data, &m); err != nil {
		return nil
	}

	return m
}

// IsDuplicate reports whether the gateway rejected the request as a
// duplicate of a recent transaction.
func (e *Error) IsDuplicate() bool {
	return e.ErrorCode == ErrCodeDuplicate
}

// IsAuth reports whether the failure is about credentials, signature,
// timestamp or IP whitelisting rather than the transaction itself.
func (e *Error) IsAuth() bool {
	switch e.ErrorCode {
	case ErrCodeIPNotWhitelisted, ErrCodeMissingHeaders, ErrCodeInvalidAPIKey, ErrCodeOriginNotRegistered, ErrCodeInvalidSignature, ErrCodeStaleTimestamp:
		return true
	}

	return e.ErrorCode < 0 // sandbox header failures
}
