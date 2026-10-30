//go:build sandbox

package business

// Live contract test against the Selcom Business sandbox. It runs only with
// the sandbox build tag and these environment variables:
//
//	SELCOM_SANDBOX_API_KEY    api-key of a sandbox credential
//	SELCOM_SANDBOX_KEY_FILE   path to its RSA private key (PEM)
//	SELCOM_SANDBOX_ORIGIN     domain registered for the credential
//	SELCOM_SANDBOX_ACCOUNT    organisation account number (balance/statement)
//
// Optional, defaulting to the sample accounts the sandbox credential page
// shows:
//
//	SELCOM_SANDBOX_RECIPIENT      Selcom account for the internal transfer (8559419110266)
//	SELCOM_SANDBOX_BANK           bank code and account (TESTBANK, 4081228192973)
//	SELCOM_SANDBOX_BANK_ACCOUNT
//	SELCOM_SANDBOX_WALLET         wallet code and number (TESTWALLET, 2557018484163481)
//	SELCOM_SANDBOX_WALLET_NUMBER
//
// The machine running it must be IP-whitelisted for the credential. Each run
// pays 1 TZS from the sandbox account to the sample Selcom account.
//
//	go test -tags sandbox -run Sandbox -v .
//
// Every raw response is printed so the documented and the real shapes can be
// compared; the typed decoding of each is then asserted.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"crypto/rand"
	"encoding/hex"
)

func sandboxClient(t *testing.T) (*Client, string) {
	t.Helper()

	apiKey := os.Getenv("SELCOM_SANDBOX_API_KEY")
	keyFile := os.Getenv("SELCOM_SANDBOX_KEY_FILE")
	account := os.Getenv("SELCOM_SANDBOX_ACCOUNT")

	if apiKey == "" || keyFile == "" || account == "" {
		t.Skip("SELCOM_SANDBOX_API_KEY, SELCOM_SANDBOX_KEY_FILE and SELCOM_SANDBOX_ACCOUNT are required")
	}

	pem, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}

	cln, err := New(apiKey, pem,
		WithBaseURL(SandboxBaseURL),
		WithOrigin(os.Getenv("SELCOM_SANDBOX_ORIGIN")),
		WithHTTPClient(&http.Client{Timeout: 30 * time.Second}),
	)
	if err != nil {
		t.Fatal(err)
	}

	return cln, account
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func newTransID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "CT-" + time.Now().UTC().Format("20060102150405") + "-" + hex.EncodeToString(b[:])
}

// raw performs a signed request exactly as the client does and returns the
// body, so the real shape can be logged next to the typed decoding.
func raw(t *testing.T, c *Client, method, path string, body any, params []field) []byte {
	t.Helper()

	var (
		req *http.Request
		err error
		fs  []field
	)

	switch method {
	case http.MethodGet:
		q := url.Values{}
		for _, f := range params {
			q.Set(f.key, f.value)
		}
		req, err = http.NewRequest(method, c.baseURL+"/"+version+path+"?"+q.Encode(), nil)
		fs = params

	default:
		data, merr := json.Marshal(body)
		if merr != nil {
			t.Fatal(merr)
		}
		fs, err = fieldsFromJSON(data)
		if err != nil {
			t.Fatal(err)
		}
		req, err = http.NewRequest(method, c.baseURL+"/"+version+path, bytes.NewReader(data))
		if req != nil {
			req.Header.Set("Content-Type", "application/json")
		}
	}
	if err != nil {
		t.Fatal(err)
	}

	ts := c.now().UTC().Format(timestampLayout)
	digest, err := c.sign(ts, fs)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("api-key", c.apiKey)
	req.Header.Set("timestamp", ts)
	req.Header.Set("digest", digest)
	req.Header.Set("signed-fields", signedFields(fs))
	if c.origin != "" {
		req.Header.Set("Origin", c.origin)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	t.Logf("RAW %s %s -> HTTP %d\n%s", method, path, resp.StatusCode, data)

	return data
}

func TestSandboxBalance(t *testing.T) {
	cln, account := sandboxClient(t)
	ctx := context.Background()

	raw(t, cln, http.MethodPost, "/balance", balanceInput{AccountNumber: account}, nil)

	resp, err := cln.Balance(ctx, account)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}

	if !resp.IsSuccess() || resp.Data.AccountNumber == "" || resp.Data.Currency == "" {
		t.Fatalf("balance decoded: %+v", resp)
	}

	t.Logf("balance %s %s active=%v", resp.Data.AvailableBalance, resp.Data.Currency, resp.Data.Active)
}

func TestSandboxLookup(t *testing.T) {
	cln, _ := sandboxClient(t)
	ctx := context.Background()

	cases := []struct {
		name, bank, account string
		mustResolve         bool
	}{
		{"selcom", DestinationSelcom, envOr("SELCOM_SANDBOX_RECIPIENT", "8559419110266"), true},
		{"bank", envOr("SELCOM_SANDBOX_BANK", "TESTBANK"), envOr("SELCOM_SANDBOX_BANK_ACCOUNT", "4081228192973"), true},
		{"wallet", envOr("SELCOM_SANDBOX_WALLET", "TESTWALLET"), envOr("SELCOM_SANDBOX_WALLET_NUMBER", "2557018484163481"), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tid := newTransID()
			raw(t, cln, http.MethodGet, "/account/lookup", nil, []field{{"bank", tc.bank}, {"account", tc.account}, {"transId", tid}})

			resp, err := cln.Lookup(ctx, LookupInput{Bank: tc.bank, Account: tc.account, TransactionID: tid, Amount: 1000})
			if err != nil {
				var apiErr *Error
				if errors.As(err, &apiErr) && !tc.mustResolve {
					t.Logf("lookup %s rejected by the sandbox: %v", tc.name, err)
					return
				}
				t.Fatalf("lookup %s: %v", tc.name, err)
			}

			if !resp.IsSuccess() || resp.Data.AccountName == "" {
				t.Fatalf("lookup %s decoded: %+v", tc.name, resp.Data)
			}

			t.Logf("lookup %s: name=%q charges=%s debit account=%s (%s)", tc.name, resp.Data.AccountName, resp.Data.TotalCharges, resp.Data.Account.Number, resp.Data.Account.Name)
		})
	}
}

func TestSandboxTransferAndStatus(t *testing.T) {
	cln, _ := sandboxClient(t)
	ctx := context.Background()

	tid := newTransID()
	in := TransferInput{
		TransactionID:    tid,
		RecipientFiCode:  DestinationSelcom,
		RecipientAccount: envOr("SELCOM_SANDBOX_RECIPIENT", "8559419110266"),
		RecipientName:    "Sandbox Selcom to Selcom",
		Amount:           1,
		Purpose:          PurposePayment,
		Remarks:          "contract test",
	}

	// Raw first with its own transId so the typed call is a fresh transaction.
	rawIn := in
	rawIn.TransactionID = newTransID()
	raw(t, cln, http.MethodPost, "/transaction/process", rawIn, nil)

	resp, err := cln.Transfer(ctx, in)
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}

	if resp.Data.TransactionID != tid || resp.Data.Status == "" || resp.ResultCode == "" {
		t.Fatalf("transfer decoded: %+v", resp)
	}

	t.Logf("transfer: result=%s code=%s status=%s receipt=%s amount=%s", resp.Result, resp.ResultCode, resp.Data.Status, resp.Data.SelcomReceipt, resp.Data.Amount)

	raw(t, cln, http.MethodGet, "/transaction/query", nil, []field{{"transId", tid}})

	st, err := cln.Status(ctx, tid)
	if err != nil {
		t.Fatalf("status: %v", err)
	}

	if st.Data.TransactionID != tid || st.Data.Status == "" {
		t.Fatalf("status decoded: %+v", st)
	}

	t.Logf("status: result=%s code=%s status=%s amount=%s receipt=%s sender=%s", st.Result, st.ResultCode, st.Data.Status, st.Data.Amount, st.Data.SelcomReceipt, st.Data.SenderAccount)

	// Duplicate transId must be refused, not paid again.
	if _, err := cln.Transfer(ctx, in); err == nil {
		t.Fatal("re-sending the same transId was accepted; expected a rejection")
	} else {
		t.Logf("duplicate transId rejected as expected: %v", err)
	}

	// Unknown transId on the status query.
	if _, err := cln.Status(ctx, "CT-does-not-exist"); err == nil {
		t.Fatal("status of an unknown transId did not error")
	} else {
		t.Logf("unknown transId: %v", err)
	}
}

// TestSandboxAsyncRails pays the sample bank and wallet accounts, which the
// gateway processes asynchronously (ACCEPTED, then settled later). It runs
// only when SELCOM_SANDBOX_RAILS lists "bank" and/or "wallet" because each
// run leaves in-progress transactions and costs sandbox balance.
func TestSandboxAsyncRails(t *testing.T) {
	cln, _ := sandboxClient(t)
	ctx := context.Background()

	rails := os.Getenv("SELCOM_SANDBOX_RAILS")
	if rails == "" {
		t.Skip("set SELCOM_SANDBOX_RAILS=bank,wallet to pay the asynchronous sample accounts")
	}

	cases := []struct {
		name, code, account, holder string
	}{
		{"bank", envOr("SELCOM_SANDBOX_BANK", "TESTBANK"), envOr("SELCOM_SANDBOX_BANK_ACCOUNT", "4081228192973"), "Sandbox Bank"},
		{"wallet", envOr("SELCOM_SANDBOX_WALLET", "TESTWALLET"), envOr("SELCOM_SANDBOX_WALLET_NUMBER", "2557018484163481"), "Sandbox Wallet"},
	}

	for _, tc := range cases {
		if !bytes.Contains([]byte(rails), []byte(tc.name)) {
			continue
		}

		t.Run(tc.name, func(t *testing.T) {
			tid := newTransID()
			in := TransferInput{TransactionID: tid, RecipientFiCode: tc.code, RecipientAccount: tc.account, RecipientName: tc.holder, Amount: 1000, Purpose: PurposePayment}

			raw(t, cln, http.MethodPost, "/transaction/process", in, nil)

			// The raw call consumed tid; send the typed call with a new one.
			in.TransactionID = newTransID()

			resp, err := cln.Transfer(ctx, in)
			if err != nil {
				t.Fatalf("transfer %s: %v", tc.name, err)
			}

			t.Logf("transfer %s: result=%s code=%s status=%s receipt=%s amount=%s principal=%s charges=%s (%s)",
				tc.name, resp.Result, resp.ResultCode, resp.Data.Status, resp.Data.SelcomReceipt, resp.Data.Amount, resp.Data.PrincipalAmount, resp.Data.TotalCharges, resp.Data.ChargesSummary)

			time.Sleep(2 * time.Second)

			st, err := cln.Status(ctx, in.TransactionID)
			if err != nil {
				t.Fatalf("status %s: %v", tc.name, err)
			}

			t.Logf("status %s after 2s: result=%s code=%s status=%s amount=%s final=%v", tc.name, st.Result, st.ResultCode, st.Data.Status, st.Data.Amount, st.IsFinal())
		})
	}
}

func TestSandboxStatement(t *testing.T) {
	cln, account := sandboxClient(t)
	ctx := context.Background()

	raw(t, cln, http.MethodPost, "/statements", StatementInput{AccountNumber: account, Preset: PresetToday, PerPage: 5}, nil)

	resp, err := cln.Statement(ctx, StatementInput{AccountNumber: account, Preset: PresetToday, PerPage: 5})
	if err != nil {
		t.Fatalf("statement: %v", err)
	}

	if !resp.IsSuccess() || resp.Data.Currency == "" {
		t.Fatalf("statement decoded: %+v", resp.Data)
	}

	t.Logf("statement: account=%s opening=%s closing=%s total=%d transactions on page=%d",
		resp.Data.Account.AccountNumber, resp.Data.OpeningBalance, resp.Data.ClosingBalance, resp.Data.Pagination.Total, len(resp.Data.Transactions))

	if len(resp.Data.Transactions) > 0 {
		tx := resp.Data.Transactions[0]
		if tx.Receipt == "" || tx.Amount == 0 || tx.TransDatetime == "" {
			b, _ := json.MarshalIndent(tx, "", "  ")
			t.Fatalf("statement transaction decoded incompletely:\n%s", b)
		}
		t.Logf("first transaction: %s %s %s %s receipt=%s", tx.TransDatetime, tx.AmountType, tx.Amount, tx.Currency, tx.Receipt)
	}

	exp, err := cln.Statement(ctx, StatementInput{AccountNumber: account, Preset: PresetToday, ExportType: ExportCSV})
	if err != nil {
		t.Fatalf("statement export: %v", err)
	}

	if !exp.Data.IsExport() {
		t.Fatalf("export decoded without a file url: %+v", exp.Data)
	}

	t.Logf("export: %s expires %s", exp.Data.FileURL, exp.Data.ExpiresAt)
}

// Compile-time guard: fmt is used by the helpers above only in some branches.
var _ = fmt.Sprintf
