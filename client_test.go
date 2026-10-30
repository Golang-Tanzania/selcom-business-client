package business

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// testKey is generated once per test binary; 2048 bits keeps the suite fast
// enough while matching what the portal issues.
var testKey = func() *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return k
}()

func testKeyPEM(t *testing.T, pkcs8 bool) []byte {
	t.Helper()

	if pkcs8 {
		der, err := x509.MarshalPKCS8PrivateKey(testKey)
		if err != nil {
			t.Fatal(err)
		}
		return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	}

	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(testKey)})
}

// fixedNow is 06:01:03.273Z expressed in a +03:00 zone so the test proves the
// client converts to UTC before formatting.
var fixedNow = time.Date(2026, 5, 27, 9, 1, 3, 273_000_000, time.FixedZone("EAT", 3*3600))

// captured is what the fake gateway saw for the last request.
type captured struct {
	method, path, rawQuery string
	headers                http.Header
	body                   []byte
}

// verifyingServer checks every request the way the gateway does: required
// headers, timestamp format, signed-fields order against the actual payload,
// and the RSA-SHA256 signature over the reconstructed signing string.
func verifyingServer(t *testing.T, status int, reply string, got *captured) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		*got = captured{method: r.Method, path: r.URL.Path, rawQuery: r.URL.RawQuery, headers: r.Header.Clone(), body: body}

		for _, h := range []string{"api-key", "timestamp", "digest", "signed-fields"} {
			if r.Header.Get(h) == "" {
				t.Errorf("missing header %s", h)
			}
		}

		ts := r.Header.Get("timestamp")
		if _, err := time.Parse(timestampLayout, ts); err != nil || !strings.HasSuffix(ts, "Z") || len(ts) != 24 {
			t.Errorf("timestamp %q is not ISO 8601 UTC with milliseconds", ts)
		}

		if r.Method == http.MethodPost && r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content-type %q", r.Header.Get("Content-Type"))
		}

		// Rebuild the signing string from what was actually sent, the way a
		// server would: decode the JSON and stringify each native value.
		// This deliberately does not reuse the library's own field walker.
		values := map[string]string{}
		switch r.Method {
		case http.MethodGet:
			for k, v := range r.URL.Query() {
				values[k] = v[0]
			}
		default:
			dec := json.NewDecoder(strings.NewReader(string(body)))
			dec.UseNumber()
			var m map[string]any
			if err := dec.Decode(&m); err != nil {
				t.Errorf("body is not a JSON object: %v", err)
			}
			for k, v := range m {
				switch x := v.(type) {
				case string:
					values[k] = x
				case json.Number:
					values[k] = x.String()
				default:
					values[k] = fmt.Sprint(x)
				}
			}
		}

		signing := "timestamp=" + ts
		for _, k := range strings.Split(r.Header.Get("signed-fields"), ",") {
			v, ok := values[k]
			if !ok {
				t.Errorf("signed field %q not present in request", k)
			}
			signing += "&" + k + "=" + v
		}

		if len(values) != len(strings.Split(r.Header.Get("signed-fields"), ",")) {
			t.Errorf("signed-fields %q does not cover all %d request fields", r.Header.Get("signed-fields"), len(values))
		}

		sig, err := base64.StdEncoding.DecodeString(r.Header.Get("digest"))
		if err != nil {
			t.Errorf("digest is not base64: %v", err)
		}

		sum := sha256.Sum256([]byte(signing))
		if err := rsa.VerifyPKCS1v15(&testKey.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
			t.Errorf("digest does not verify over %q: %v", signing, err)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
}

func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()

	cln, err := New("a1b2c3", testKeyPEM(t, false), WithBaseURL(srv.URL), withClock(func() time.Time { return fixedNow }))
	if err != nil {
		t.Fatal(err)
	}

	return cln
}

func TestOriginHeader(t *testing.T) {
	var got captured

	srv := verifyingServer(t, 200, `{"success":true,"error_code":1,"result":"SUCCESS","resultcode":"000","data":{"status":"COMPLETED"}}`, &got)
	defer srv.Close()

	cln, err := New("k", testKeyPEM(t, false), WithBaseURL(srv.URL), WithOrigin("https://api.example.com"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := cln.Status(context.Background(), "T1"); err != nil {
		t.Fatal(err)
	}

	if got.headers.Get("Origin") != "https://api.example.com" {
		t.Fatalf("Origin header: %q", got.headers.Get("Origin"))
	}

	// Without the option no Origin header is sent at all.
	if _, err := newTestClient(t, srv).Status(context.Background(), "T1"); err != nil {
		t.Fatal(err)
	}

	if _, ok := got.headers["Origin"]; ok {
		t.Fatal("Origin must not be sent when not configured")
	}
}

func TestNew(t *testing.T) {
	if _, err := New("k", testKeyPEM(t, true)); err != nil {
		t.Fatalf("pkcs8 key rejected: %v", err)
	}

	if _, err := New("", testKeyPEM(t, false)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty api key: got %v", err)
	}

	if _, err := New("k", []byte("not a key")); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("garbage key: got %v", err)
	}

	cln, _ := New("k", testKeyPEM(t, false), WithBaseURL("https://sandbox.selcom.business/v1/"))
	if cln.BaseURL() != SandboxBaseURL {
		t.Fatalf("base url: got %q", cln.BaseURL())
	}
}

func TestTransfer(t *testing.T) {
	var got captured

	srv := verifyingServer(t, 200, `{"success":true,"error_code":1,"message":"Transaction processed successfully.",
		"result":"INPROGRESS","resultcode":"111",
		"data":{"trans_id":"TXN1","selcom_receipt":"SLK-1","status":"ACCEPTED","amount":2500000,"currency":"TZS"}}`, &got)
	defer srv.Close()

	cln := newTestClient(t, srv)

	resp, err := cln.Transfer(context.Background(), TransferInput{
		TransactionID:    "TXN1",
		RecipientFiCode:  DestinationCRDB,
		RecipientAccount: "0150123456789",
		RecipientName:    "Jane Doe",
		Amount:           2500000,
		Purpose:          PurposeSalaryOrWages,
		Remarks:          "October payroll",
	})
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}

	if got.method != http.MethodPost || got.path != "/v1/transaction/process" {
		t.Fatalf("request: %s %s", got.method, got.path)
	}

	if sf := got.headers.Get("signed-fields"); sf != "transId,recipientFiCode,recipientAccount,recipientName,amount,purpose,remarks" {
		t.Fatalf("signed-fields: %s", sf)
	}

	if got.headers.Get("timestamp") != "2026-05-27T06:01:03.273Z" {
		t.Fatalf("timestamp: %s", got.headers.Get("timestamp"))
	}

	if !strings.Contains(string(got.body), `"amount":2500000`) {
		t.Fatalf("amount must be a JSON number: %s", got.body)
	}

	if !resp.IsInProgress() || resp.IsFinal() || resp.Data.Status != TransactionAccepted || resp.Data.SelcomReceipt != "SLK-1" {
		t.Fatalf("response: %+v", resp)
	}
}

func TestTransferOmitsEmptyRemarks(t *testing.T) {
	var got captured

	srv := verifyingServer(t, 200, `{"success":true,"error_code":1,"result":"SUCCESS","resultcode":"000","data":{"status":"COMPLETED"}}`, &got)
	defer srv.Close()

	cln := newTestClient(t, srv)

	resp, err := cln.Transfer(context.Background(), TransferInput{
		TransactionID: "TXN2", RecipientFiCode: DestinationSelcom, RecipientAccount: "9876543210",
		RecipientName: "Alex", Amount: 100, Purpose: PurposeFundsTransfer,
	})
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}

	if sf := got.headers.Get("signed-fields"); strings.Contains(sf, "remarks") {
		t.Fatalf("empty remarks must not be signed or sent: %s", sf)
	}

	if !resp.IsSuccess() || !resp.IsFinal() || resp.Data.Status != TransactionCompleted {
		t.Fatalf("response: %+v", resp)
	}
}

func TestTransferValidation(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	cln := newTestClient(t, srv)

	cases := map[string]TransferInput{
		"no transId":   {RecipientFiCode: "CRDB", RecipientAccount: "1", RecipientName: "n", Amount: 1, Purpose: "FT"},
		"no fi code":   {TransactionID: "t", RecipientAccount: "1", RecipientName: "n", Amount: 1, Purpose: "FT"},
		"zero amount":  {TransactionID: "t", RecipientFiCode: "CRDB", RecipientAccount: "1", RecipientName: "n", Amount: 0, Purpose: "FT"},
		"no purpose":   {TransactionID: "t", RecipientFiCode: "CRDB", RecipientAccount: "1", RecipientName: "n", Amount: 1},
		"long remarks": {TransactionID: "t", RecipientFiCode: "CRDB", RecipientAccount: "1", RecipientName: "n", Amount: 1, Purpose: "FT", Remarks: strings.Repeat("x", 141)},
	}

	// Limits count characters, not bytes: 100 two-byte runes are within the limit.
	okName := TransferInput{TransactionID: "t", RecipientFiCode: "CRDB", RecipientAccount: "1", RecipientName: strings.Repeat("Ñ", 100), Amount: 1, Purpose: "FT"}
	if err := okName.validate(); err != nil {
		t.Errorf("100-rune name rejected: %v", err)
	}

	for name, in := range cases {
		if _, err := cln.Transfer(context.Background(), in); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

func TestLookup(t *testing.T) {
	var got captured

	srv := verifyingServer(t, 200, `{"success":true,"error_code":1,"message":"ok","transId":"L1","result":"SUCCESS","resultcode":"000",
		"data":{"bank":"CRDB","account":"0150","accountName":"JANE DOE","operator":"CRDB Bank","charges":[{"name":"fee","amount":500}],"totalCharges":500,"categoryCode":"B"}}`, &got)
	defer srv.Close()

	cln := newTestClient(t, srv)

	resp, err := cln.Lookup(context.Background(), LookupInput{Bank: DestinationCRDB, Account: "0150", TransactionID: "L1", Amount: 50000})
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}

	if got.method != http.MethodGet || got.path != "/v1/account/lookup" {
		t.Fatalf("request: %s %s", got.method, got.path)
	}

	if sf := got.headers.Get("signed-fields"); sf != "bank,account,transId,amount" {
		t.Fatalf("signed-fields: %s", sf)
	}

	if resp.Data.AccountName != "JANE DOE" || resp.TransactionID != "L1" || resp.Data.TotalCharges != 500 || resp.Data.Account.Number != "0150" {
		t.Fatalf("response: %+v", resp)
	}

	// Without an amount the field is neither sent nor signed.
	if _, err := cln.Lookup(context.Background(), LookupInput{Bank: "NMB", Account: "1", TransactionID: "L2"}); err != nil {
		t.Fatal(err)
	}

	if sf := got.headers.Get("signed-fields"); sf != "bank,account,transId" {
		t.Fatalf("signed-fields without amount: %s", sf)
	}
}

func TestStatusDecodesStringAmounts(t *testing.T) {
	// Captured from the sandbox on 2026-10-30: amount comes back as a string.
	body := `{"success":true,"error_code":1,"message":"ok","result":"INPROGRESS","resultcode":"111",
		"data":{"transId":"T1","status":"ACCEPTED","amount":"1000","currency":"TZS","selcomReceipt":"SBS-1","transDatetime":"2026-10-30 21:48:34"}}`

	var got captured
	srv := verifyingServer(t, 200, body, &got)
	defer srv.Close()

	resp, err := newTestClient(t, srv).Status(context.Background(), "T1")
	if err != nil {
		t.Fatalf("status: %v", err)
	}

	if resp.Data.Amount != 1000 || resp.Data.Amount.String() != "1000" || resp.Data.Status != TransactionAccepted {
		t.Fatalf("decoded: %+v", resp.Data)
	}

	for in, want := range map[string]float64{`"300.5"`: 300.5, `250`: 250, `null`: 0, `""`: 0} {
		var n Number
		if err := json.Unmarshal([]byte(in), &n); err != nil || float64(n) != want {
			t.Errorf("Number %s: got %v err %v", in, n, err)
		}
	}
}

func TestLookupDecodesObservedSandboxShape(t *testing.T) {
	// Captured from the sandbox on 2026-10-30: account is an object describing
	// the debit account, operator is null, charge amounts are strings.
	body := `{"success":true,"error_code":1,"message":"Lookup successful.","transId":"ffbc9a10","result":"SUCCESS","resultcode":"000",
		"data":{"bank":"TESTBANK","account":{"id":20,"organization_id":841,"account_number":"9094544901413","account_name":"Sandbox Test","created_at":"2026-10-30T13:38:09.000000Z","updated_at":"2026-10-30T13:38:09.000000Z"},
		"accountName":"Sandbox Bank","operator":null,"charges":[{"charge_name":"Charge","charge_amount":"300"}],"totalCharges":300}}`

	var got captured
	srv := verifyingServer(t, 200, body, &got)
	defer srv.Close()

	resp, err := newTestClient(t, srv).Lookup(context.Background(), LookupInput{Bank: "TESTBANK", Account: "4081228192973", TransactionID: "L3"})
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}

	if resp.Data.AccountName != "Sandbox Bank" || resp.Data.Account.Number != "9094544901413" || resp.Data.Account.Name != "Sandbox Test" ||
		resp.Data.Operator != "" || resp.Data.TotalCharges != 300 || len(resp.Data.Charges) != 1 || resp.Data.Charges[0]["charge_amount"] != "300" {
		t.Fatalf("decoded: %+v", resp.Data)
	}
}

func TestStatus(t *testing.T) {
	var got captured

	srv := verifyingServer(t, 200, `{"success":true,"error_code":1,"result":"SUCCESS","resultcode":"000",
		"data":{"transId":"TXN1","status":"COMPLETED","amount":100,"currency":"TZS","selcomReceipt":"SLK-1","transDatetime":"2026-05-27 06:01:03","senderAccount":"0001","senderName":"Acme"}}`, &got)
	defer srv.Close()

	cln := newTestClient(t, srv)

	resp, err := cln.Status(context.Background(), "TXN1")
	if err != nil {
		t.Fatalf("status: %v", err)
	}

	if got.path != "/v1/transaction/query" || got.rawQuery != "transId=TXN1" || got.headers.Get("signed-fields") != "transId" {
		t.Fatalf("request: %s?%s signed=%s", got.path, got.rawQuery, got.headers.Get("signed-fields"))
	}

	if resp.Data.Status != TransactionCompleted || resp.Data.SenderName != "Acme" {
		t.Fatalf("response: %+v", resp)
	}
}

func TestBalance(t *testing.T) {
	var got captured

	srv := verifyingServer(t, 200, `{"success":true,"error_code":1,"message":"Balance fetched successfully.","result":"SUCCESS","resultcode":"000",
		"data":{"account_number":"000011636","currency":"TZS","available_balance":100000000,"active":true}}`, &got)
	defer srv.Close()

	cln := newTestClient(t, srv)

	resp, err := cln.Balance(context.Background(), "000011636")
	if err != nil {
		t.Fatalf("balance: %v", err)
	}

	if got.path != "/v1/balance" || got.headers.Get("signed-fields") != "account_number" {
		t.Fatalf("request: %s signed=%s", got.path, got.headers.Get("signed-fields"))
	}

	if resp.Data.AvailableBalance != 100000000 || !resp.Data.Active {
		t.Fatalf("response: %+v", resp)
	}
}

func TestStatement(t *testing.T) {
	var got captured

	srv := verifyingServer(t, 200, `{"success":true,"error_code":1,"result":"SUCCESS","resultcode":"000",
		"data":{"export_type":"pdf","file_url":"https://x/statement.pdf","expires_at":"2026-05-27T07:01:03+00:00","filters_applied":{"from_date":"2026-05-01","to_date":"2026-05-27"}}}`, &got)
	defer srv.Close()

	cln := newTestClient(t, srv)

	resp, err := cln.Statement(context.Background(), StatementInput{AccountNumber: "0123", FromDate: "2026-05-01", ToDate: "2026-05-27", ExportType: ExportPDF})
	if err != nil {
		t.Fatalf("statement: %v", err)
	}

	if got.path != "/v1/statements" || got.headers.Get("signed-fields") != "account_number,from_date,to_date,export_type" {
		t.Fatalf("request: %s signed=%s", got.path, got.headers.Get("signed-fields"))
	}

	if !resp.Data.IsExport() || resp.Data.FileURL == "" {
		t.Fatalf("response: %+v", resp)
	}

	bad := []StatementInput{
		{},
		{AccountNumber: "1", Preset: PresetToday, FromDate: "2026-01-01", ToDate: "2026-01-02"},
		{AccountNumber: "1", FromDate: "2026-01-01"},
		{AccountNumber: "1", PerPage: 501},
		{AccountNumber: "1", Order: "UP"},
	}

	for i, in := range bad {
		if _, err := cln.Statement(context.Background(), in); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("case %d: got %v", i, err)
		}
	}
}

func TestGatewayErrors(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		body      string
		code      int
		auth, dup bool
		fields    bool
	}{
		{"validation", 422, `{"success":false,"error_code":651,"message":"Validation failed.","result":"FAIL","resultcode":"651","data":{"purpose":["The selected purpose is invalid."]}}`, 651, false, false, true},
		{"duplicate", 422, `{"success":false,"error_code":643,"message":"Duplicate transaction detected.","result":"FAIL","resultcode":"643","data":[]}`, 643, false, true, false},
		{"sandbox duplicate", 422, `{"success":false,"error_code":-34,"message":"Duplicate transaction detected.","result":"FAIL","resultcode":"-34","data":[]}`, -34, true, true, false},
		{"bad signature", 401, `{"success":false,"error_code":631,"message":"digest does not match","result":"FAIL","resultcode":"631","data":[]}`, 631, true, false, false},
		{"sandbox missing header", 400, `{"success":false,"error_code":-11,"message":"Missing required header: api-key.","data":[]}`, -11, true, false, false},
		{"html from a proxy", 502, `<html>bad gateway</html>`, 0, false, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got captured
			srv := verifyingServer(t, tc.status, tc.body, &got)
			defer srv.Close()

			cln := newTestClient(t, srv)

			_, err := cln.Status(context.Background(), "TXN1")

			var apiErr *Error
			if !errors.As(err, &apiErr) {
				t.Fatalf("want *Error, got %v", err)
			}

			if apiErr.HTTPStatus != tc.status || apiErr.ErrorCode != tc.code {
				t.Fatalf("error: %+v", apiErr)
			}

			if apiErr.IsAuth() != tc.auth || apiErr.IsDuplicate() != tc.dup {
				t.Fatalf("classification: auth=%v dup=%v for %+v", apiErr.IsAuth(), apiErr.IsDuplicate(), apiErr)
			}

			if fe := apiErr.FieldErrors(); (fe != nil) != tc.fields {
				t.Fatalf("field errors: %v", fe)
			}

			if IsRetryable(err) != (tc.status >= 500) {
				t.Fatalf("retryable: %v", IsRetryable(err))
			}
		})
	}
}

func TestSigningStringMatchesPortalExample(t *testing.T) {
	fields := []field{
		{"transId", "1234567899"},
		{"recipientFiCode", "SELCOM"},
		{"recipientAccount", "255711410410"},
		{"amount", "100"},
	}

	want := "timestamp=2026-05-27T06:01:03.273Z&transId=1234567899&recipientFiCode=SELCOM&recipientAccount=255711410410&amount=100"
	if got := signingString("2026-05-27T06:01:03.273Z", fields); got != want {
		t.Fatalf("signing string:\n got %s\nwant %s", got, want)
	}
}

func TestFieldsFromJSONKeepsLiterals(t *testing.T) {
	fields, err := fieldsFromJSON([]byte(`{"transId":"01234","amount":1000000,"flag":true,"note":"a b"}`))
	if err != nil {
		t.Fatal(err)
	}

	want := []field{{"transId", "01234"}, {"amount", "1000000"}, {"flag", "true"}, {"note", "a b"}}
	if len(fields) != len(want) {
		t.Fatalf("fields: %+v", fields)
	}

	for i := range want {
		if fields[i] != want[i] {
			t.Fatalf("field %d: got %+v want %+v", i, fields[i], want[i])
		}
	}

	if _, err := fieldsFromJSON([]byte(`{"nested":{"a":1}}`)); err == nil {
		t.Fatal("nested values must be rejected")
	}
}

func TestOutcomeHelpers(t *testing.T) {
	cases := map[string][4]bool{ // success, inprogress, ambiguous, final
		"000": {true, false, false, true},
		"111": {false, true, false, false},
		"927": {false, true, false, false},
		"999": {false, false, true, false},
		"642": {false, false, false, true},
	}

	for code, want := range cases {
		o := Outcome{ResultCode: code}
		got := [4]bool{o.IsSuccess(), o.IsInProgress(), o.IsAmbiguous(), o.IsFinal()}
		if got != want {
			t.Errorf("%s: got %v want %v", code, got, want)
		}
		if o.IsFailed() != (!want[0] && !want[1] && !want[2]) {
			t.Errorf("%s: failed=%v", code, o.IsFailed())
		}
	}
}

func TestParseCallback(t *testing.T) {
	body := `{"reference_id":"2024061500123","status":"SUCCESS","recipient_name":"John Doe","amount":50000,"charges":500,"selcom_receipt":"202406150001"}`

	req := httptest.NewRequest(http.MethodPost, "/callback", strings.NewReader(body))

	cb, err := ParseCallback(req)
	if err != nil {
		t.Fatal(err)
	}

	if cb.ReferenceID != "2024061500123" || cb.Amount != 50000 || cb.SelcomReceipt != "202406150001" {
		t.Fatalf("callback: %+v", cb)
	}

	if _, err := ParseCallback(httptest.NewRequest(http.MethodGet, "/callback", nil)); err == nil {
		t.Fatal("GET must be rejected")
	}

	if _, err := ParseCallback(httptest.NewRequest(http.MethodPost, "/callback", strings.NewReader(`{"status":"SUCCESS"}`))); err == nil {
		t.Fatal("missing reference_id must be rejected")
	}
}

func TestSpecialCharactersAreSignedAsSent(t *testing.T) {
	var got captured

	srv := verifyingServer(t, 200, `{"success":true,"error_code":1,"result":"SUCCESS","resultcode":"000","data":{"status":"COMPLETED"}}`, &got)
	defer srv.Close()

	cln := newTestClient(t, srv)

	// & < > = and non-ASCII must be signed as the decoded value even though
	// encoding/json escapes some of them on the wire.
	_, err := cln.Transfer(context.Background(), TransferInput{
		TransactionID: "T&1=2", RecipientFiCode: DestinationMPesa, RecipientAccount: "255700000000",
		RecipientName: "Ñandú <Ltd> & Sons", Amount: 1, Purpose: PurposePayment, Remarks: "a=b&c",
	})
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}

	if !strings.Contains(string(got.body), `\u0026`) {
		t.Fatalf("expected encoding/json to escape & on the wire: %s", got.body)
	}
}

func TestOKWithFailResultAndEmptyData(t *testing.T) {
	var got captured

	srv := verifyingServer(t, 200, `{"success":false,"error_code":642,"message":"Transaction processing failed at the bank.","result":"FAIL","resultcode":"642","data":[]}`, &got)
	defer srv.Close()

	cln := newTestClient(t, srv)

	resp, err := cln.Transfer(context.Background(), TransferInput{
		TransactionID: "T1", RecipientFiCode: DestinationCRDB, RecipientAccount: "1", RecipientName: "n", Amount: 1, Purpose: PurposeFundsTransfer,
	})
	if err != nil {
		t.Fatalf("a 200 with empty data must decode: %v", err)
	}

	if !resp.IsFailed() || !resp.IsFinal() || resp.ErrorCode != 642 || resp.Message == "" {
		t.Fatalf("outcome: %+v", resp.Outcome)
	}

	// Top-level members outside the envelope survive the data rewrite.
	srv2 := verifyingServer(t, 200, `{"success":true,"error_code":1,"transId":"L9","result":"SUCCESS","resultcode":"000","data":null}`, &got)
	defer srv2.Close()

	lk, err := newTestClient(t, srv2).Lookup(context.Background(), LookupInput{Bank: "NMB", Account: "1", TransactionID: "L9"})
	if err != nil || lk.TransactionID != "L9" {
		t.Fatalf("lookup with null data: %v %+v", err, lk)
	}
}

func TestIsRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	cln := newTestClient(t, srv)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := cln.Status(ctx, "T1")
	if err == nil || IsRetryable(err) {
		t.Fatalf("a deadline-exceeded request must not be retryable: %v", err)
	}

	srv.Close()

	_, err = cln.Status(context.Background(), "T1")
	if err == nil || !IsRetryable(err) {
		t.Fatalf("a connection failure must be retryable: %v", err)
	}

	if IsRetryable(&Error{HTTPStatus: 422}) || !IsRetryable(&Error{HTTPStatus: 503}) {
		t.Fatal("http status classification")
	}
}

func TestStatementListing(t *testing.T) {
	var got captured

	srv := verifyingServer(t, 200, `{"success":true,"error_code":1,"result":"SUCCESS","resultcode":"000","data":{
		"filters_applied":{"account_number":"0123","preset":"This Month","from_date":"2025-07-01","to_date":"2025-07-31","per_page":10,"order":"DESC"},
		"account":{"account_name":"Acme Ltd","account_number":"01234 56789","sub_account_name":"Main"},
		"currency":"TZS","opening_balance":500000,"closing_balance":450000,
		"pagination":{"total":42,"per_page":10,"current_page":1,"last_page":5,"from":1,"to":10},
		"transactions":[{"id":56,"partyName":"Sandbox Bank","statusText":"Success","trans_datetime":"2026-10-30 22:02:07","trans_amount_type":"debit","transaction_amount":"1000","transaction_currency":"TZS","ara_receipt":"SBS-1"}]}}`, &got)
	defer srv.Close()

	resp, err := newTestClient(t, srv).Statement(context.Background(), StatementInput{AccountNumber: "0123", Preset: PresetThisMonth, PerPage: 10, Order: "DESC"})
	if err != nil {
		t.Fatal(err)
	}

	if got.headers.Get("signed-fields") != "account_number,preset,per_page,order" {
		t.Fatalf("signed-fields: %s", got.headers.Get("signed-fields"))
	}

	if resp.Data.IsExport() || resp.Data.Pagination.Total != 42 || resp.Data.Account.AccountName != "Acme Ltd" || len(resp.Data.Transactions) != 1 || resp.Data.ClosingBalance != 450000 {
		t.Fatalf("listing: %+v", resp.Data)
	}

	if tx := resp.Data.Transactions[0]; tx.Receipt != "SBS-1" || tx.Amount != 1000 || tx.AmountType != "debit" {
		t.Fatalf("transaction: %+v", tx)
	}
}

func TestOutcomeWithoutResultCode(t *testing.T) {
	if o := (Outcome{Success: true, Result: "SUCCESS"}); !o.IsSuccess() || o.IsFailed() {
		t.Fatalf("SUCCESS without resultcode: %+v", o)
	}

	if o := (Outcome{Success: true}); !o.IsSuccess() {
		t.Fatalf("bare success without result: %+v", o)
	}

	if o := (Outcome{Success: false, Result: "FAIL"}); !o.IsFailed() {
		t.Fatalf("FAIL without resultcode: %+v", o)
	}
}

// Ensure the JSON encoding of the envelope types round-trips the sample
// documents from the portal without losing members.
func TestEnvelopeDecoding(t *testing.T) {
	var resp TransferResponse
	if err := json.Unmarshal([]byte(`{"success":true,"error_code":1,"message":"m","result":"SUCCESS","resultcode":"000","data":{"trans_id":"a","selcom_receipt":"b","status":"COMPLETED","amount":100000,"currency":"TZS"}}`), &resp); err != nil {
		t.Fatal(err)
	}

	if !resp.Success || resp.ErrorCode != 1 || resp.Data.TransactionID != "a" {
		t.Fatalf("decoded: %+v", resp)
	}
}
