package business

import (
	"bytes"
	"encoding/json"
	"unicode/utf8"
)

// Result codes carried in resultcode. See "API Response" in the portal.
const (
	ResultCodeSuccess     = "000"
	ResultCodeInProgress  = "111"
	ResultCodeInProgress2 = "927"
	ResultCodeAmbiguous   = "999"
)

// Transaction statuses reported in data.status by Transfer and Status.
const (
	TransactionAccepted  = "ACCEPTED"  // asynchronous; poll Status until COMPLETED or FAILED.
	TransactionCompleted = "COMPLETED" // money delivered.
	TransactionFailed    = "FAILED"    // no money moved.
)

// envelope is the common response shape.
type envelope struct {
	Success    bool            `json:"success"`
	ErrorCode  int             `json:"error_code"`
	Message    string          `json:"message"`
	Result     string          `json:"result"`
	ResultCode string          `json:"resultcode"`
	Data       json.RawMessage `json:"data"`
}

// Outcome is the part of every successful response that says how the
// request went. Only SUCCESS and FAIL are terminal: when IsInProgress or
// IsAmbiguous is true, never resubmit the transaction (with the same or a
// new transId) because the money may already have moved. Poll Status
// instead until IsFinal reports true.
type Outcome struct {
	Success    bool   `json:"success"`
	ErrorCode  int    `json:"error_code"`
	Message    string `json:"message"`
	Result     string `json:"result"`
	ResultCode string `json:"resultcode"`
}

// code returns the result code, falling back to the textual result when the
// gateway omits resultcode so that a bare "SUCCESS" is not read as a failure.
func (o Outcome) code() string {
	if o.ResultCode != "" {
		return o.ResultCode
	}

	switch o.Result {
	case "SUCCESS":
		return ResultCodeSuccess
	case "INPROGRESS":
		return ResultCodeInProgress
	case "AMBIGUOUS":
		return ResultCodeAmbiguous
	case "":
		if o.Success {
			return ResultCodeSuccess
		}
	}

	return o.ResultCode
}

// IsSuccess reports result code 000.
func (o Outcome) IsSuccess() bool { return o.code() == ResultCodeSuccess }

// IsInProgress reports result codes 111 and 927.
func (o Outcome) IsInProgress() bool {
	c := o.code()
	return c == ResultCodeInProgress || c == ResultCodeInProgress2
}

// IsAmbiguous reports result code 999: outcome unknown, wait for
// reconciliation and poll.
func (o Outcome) IsAmbiguous() bool { return o.code() == ResultCodeAmbiguous }

// IsFailed reports every code other than 000, 111, 927 and 999.
func (o Outcome) IsFailed() bool {
	return !o.IsSuccess() && !o.IsInProgress() && !o.IsAmbiguous()
}

// IsFinal reports whether polling can stop.
func (o Outcome) IsFinal() bool { return !o.IsInProgress() && !o.IsAmbiguous() }

// =============================================================================

// TransferInput is the payload of POST /v1/transaction/process. Field order
// is the signing order.
type TransferInput struct {
	// TransactionID is your unique reference, used for idempotency and
	// status tracking. Reuse of a recent value is rejected with error 643.
	TransactionID string `json:"transId"`
	// RecipientFiCode is the destination institution, see Destination*.
	RecipientFiCode string `json:"recipientFiCode"`
	// RecipientAccount is the account number or mobile number.
	RecipientAccount string `json:"recipientAccount"`
	// RecipientName is the full name, at most 100 characters. Use Lookup to
	// obtain it.
	RecipientName string `json:"recipientName"`
	// Amount in whole TZS, at least 1. The gateway accepts a numeric value;
	// an integer is used because TZS has no fractional unit in practice.
	Amount int `json:"amount"`
	// Purpose is one of the Purpose* codes.
	Purpose string `json:"purpose"`
	// Remarks is optional free text, at most 140 characters, shown on
	// statements.
	Remarks string `json:"remarks,omitempty"`
}

func (in TransferInput) validate() error {
	const op = "transfer"

	switch {
	case in.TransactionID == "":
		return invalidInput(op, "transId is required")
	case in.RecipientFiCode == "":
		return invalidInput(op, "recipientFiCode is required")
	case in.RecipientAccount == "":
		return invalidInput(op, "recipientAccount is required")
	case in.RecipientName == "":
		return invalidInput(op, "recipientName is required")
	case utf8.RuneCountInString(in.RecipientName) > 100:
		return invalidInput(op, "recipientName exceeds 100 characters")
	case in.Amount < 1:
		return invalidInput(op, "amount must be at least 1")
	case in.Purpose == "":
		return invalidInput(op, "purpose is required")
	case utf8.RuneCountInString(in.Remarks) > 140:
		return invalidInput(op, "remarks exceeds 140 characters")
	}

	return nil
}

// TransferData is data of a transfer response.
type TransferData struct {
	TransactionID string  `json:"trans_id"`
	SelcomReceipt string  `json:"selcom_receipt"`
	Status        string  `json:"status"` // ACCEPTED or COMPLETED
	Amount        float64 `json:"amount"`
	Currency      string  `json:"currency"`
}

// TransferResponse is returned by Transfer.
type TransferResponse struct {
	Outcome
	Data TransferData `json:"data"`
}

// =============================================================================

// LookupInput is the query of GET /v1/account/lookup.
type LookupInput struct {
	// Bank is the destination institution code, see Destination*.
	Bank string
	// Account is the account or mobile number to resolve.
	Account string
	// TransactionID is a unique reference for the lookup. It may be reused
	// as the transfer's TransactionID.
	TransactionID string
	// Amount is optional; when set the response includes the charges that
	// would apply to a transfer of that amount.
	Amount int
}

// Charge is one line of a lookup's charge breakdown. The gateway returns
// objects such as {"charge_name": "Charge", "charge_amount": "300"}; the
// members are not documented, so it is kept generic.
type Charge map[string]any

// LookupAccount is the account member of a lookup response. The portal
// documents it as the looked-up account number (a string), but the gateway
// returns an object describing the organisation's own debit account:
// {"account_number": "...", "account_name": "...", ...}. Both forms are
// accepted; the recipient's name is in LookupData.AccountName either way.
type LookupAccount struct {
	Number string `json:"account_number"`
	Name   string `json:"account_name"`
}

// UnmarshalJSON accepts either a JSON string (the documented form) or an
// object (the observed form).
func (a *LookupAccount) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		return nil
	}

	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*a = LookupAccount{Number: s}
		return nil
	}

	type raw LookupAccount
	var r raw
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	*a = LookupAccount(r)

	return nil
}

// LookupData is data of a lookup response.
type LookupData struct {
	Bank         string        `json:"bank"`
	Account      LookupAccount `json:"account"`
	AccountName  string        `json:"accountName"` // The recipient's name.
	Operator     string        `json:"operator"`    // May be null; empty then.
	Charges      []Charge      `json:"charges"`
	TotalCharges float64       `json:"totalCharges"`
	CategoryCode string        `json:"categoryCode"`
}

// LookupResponse is returned by Lookup.
type LookupResponse struct {
	Outcome
	TransactionID string     `json:"transId"`
	Data          LookupData `json:"data"`
}

// =============================================================================

// StatusData is data of a status response.
type StatusData struct {
	TransactionID string  `json:"transId"`
	Status        string  `json:"status"` // ACCEPTED, COMPLETED or FAILED
	Amount        float64 `json:"amount"`
	Currency      string  `json:"currency"`
	SelcomReceipt string  `json:"selcomReceipt"`
	TransDatetime string  `json:"transDatetime"` // Y-m-d H:i:s
	SenderAccount string  `json:"senderAccount"`
	SenderName    string  `json:"senderName"`
}

// StatusResponse is returned by Status.
type StatusResponse struct {
	Outcome
	Data StatusData `json:"data"`
}

// =============================================================================

// BalanceData is data of a balance response.
type BalanceData struct {
	AccountNumber    string  `json:"account_number"`
	Currency         string  `json:"currency"`
	AvailableBalance float64 `json:"available_balance"`
	Active           bool    `json:"active"`
}

// BalanceResponse is returned by Balance.
type BalanceResponse struct {
	Outcome
	Data BalanceData `json:"data"`
}

// =============================================================================

// Statement presets accepted by StatementInput.Preset.
const (
	PresetToday              = "Today"
	PresetLast7Days          = "Last 7 Days"
	PresetThisMonth          = "This Month"
	PresetLastMonth          = "Last Month"
	PresetLast10Transactions = "Last 10 Transactions"
)

// Statement export types accepted by StatementInput.ExportType.
const (
	ExportPDF  = "pdf"
	ExportXLSX = "xlsx"
	ExportCSV  = "csv"
	ExportJSON = "json"
)

// StatementInput is the payload of POST /v1/statements. Give either Preset
// or both FromDate and ToDate (Y-m-d); the two are mutually exclusive and
// neither yields an empty result. Field order is the signing order.
type StatementInput struct {
	AccountNumber string `json:"account_number"`
	Preset        string `json:"preset,omitempty"`
	FromDate      string `json:"from_date,omitempty"`
	ToDate        string `json:"to_date,omitempty"`
	PerPage       int    `json:"per_page,omitempty"` // 1..500, default 10
	Order         string `json:"order,omitempty"`    // ASC or DESC, default DESC
	ExportType    string `json:"export_type,omitempty"`
}

func (in StatementInput) validate() error {
	const op = "statement"

	switch {
	case in.AccountNumber == "":
		return invalidInput(op, "account_number is required")
	case in.Preset != "" && (in.FromDate != "" || in.ToDate != ""):
		return invalidInput(op, "preset and from_date/to_date are mutually exclusive")
	case (in.FromDate == "") != (in.ToDate == ""):
		return invalidInput(op, "from_date and to_date must be given together")
	case in.PerPage < 0 || in.PerPage > 500:
		return invalidInput(op, "per_page must be between 1 and 500")
	case in.Order != "" && in.Order != "ASC" && in.Order != "DESC":
		return invalidInput(op, "order must be ASC or DESC")
	}

	return nil
}

// StatementFilters echoes the filters the gateway resolved.
type StatementFilters struct {
	AccountNumber string `json:"account_number"`
	Preset        string `json:"preset"`
	FromDate      string `json:"from_date"`
	ToDate        string `json:"to_date"`
	PerPage       int    `json:"per_page"`
	Order         string `json:"order"`
}

// StatementAccount describes the account a statement belongs to.
type StatementAccount struct {
	AccountName    string `json:"account_name"`
	AccountNumber  string `json:"account_number"`
	SubAccountName string `json:"sub_account_name"`
}

// Pagination describes the page of a statement listing.
type Pagination struct {
	Total       int `json:"total"`
	PerPage     int `json:"per_page"`
	CurrentPage int `json:"current_page"`
	LastPage    int `json:"last_page"`
	From        int `json:"from"`
	To          int `json:"to"`
}

// StatementData is data of a statement response. For a listing the
// account, balances, pagination and transactions are set; for a file export
// ExportType, FileURL and ExpiresAt are set instead.
type StatementData struct {
	FiltersApplied StatementFilters `json:"filters_applied"`
	Account        StatementAccount `json:"account"`
	Currency       string           `json:"currency"`
	OpeningBalance float64          `json:"opening_balance"`
	ClosingBalance float64          `json:"closing_balance"`
	Pagination     Pagination       `json:"pagination"`
	// Transactions are kept generic because the portal does not document
	// their members.
	Transactions []map[string]any `json:"transactions"`

	ExportType string `json:"export_type"`
	FileURL    string `json:"file_url"`
	ExpiresAt  string `json:"expires_at"` // ISO 8601; the link lives one hour.
}

// IsExport reports whether the response carries a download link rather
// than a listing.
func (d StatementData) IsExport() bool { return d.FileURL != "" }

// StatementResponse is returned by Statement.
type StatementResponse struct {
	Outcome
	Data StatementData `json:"data"`
}
