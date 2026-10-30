# Selcom Business Go Client

[![Go Reference](https://pkg.go.dev/badge/github.com/Golang-Tanzania/selcom-business-client.svg)](https://pkg.go.dev/github.com/Golang-Tanzania/selcom-business-client)
[![Go Report Card](https://goreportcard.com/badge/github.com/Golang-Tanzania/selcom-business-client)](https://goreportcard.com/report/github.com/Golang-Tanzania/selcom-business-client)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

A Go client for the [Selcom Business API Gateway v1](https://developer.selcom.business)
send money to banks, mobile wallets and Selcom accounts, look up recipients,
track transactions, and read balances and statements.

Standard library only. Go 1.23 or later.

> **Not the Selcom Pay gateway.** This client covers the Selcom *Business*
> gateway (`api.selcom.business`), which uses RSA request signing and one
> transfer endpoint for every destination. Checkout and collections live on
> the legacy Selcom Pay gateway; use
> [`selcompay-client`](https://github.com/Golang-Tanzania/selcompay-client) for those.

## Validation status

Validated against Selcom's **sandbox** on 30 October 2026 with real
credentials: request signing and the `Origin` check are accepted, and
balance, recipient lookup, transfer (synchronous Selcom-to-Selcom and
asynchronous bank), transaction status, duplicate detection, unknown-id
handling, statement listing and export all decode correctly from the
gateway's actual responses. Several of those differ from the portal's
tables (object instead of string, string instead of number, undocumented
members); the models follow what the gateway sends, and the
[sandbox contract test](#sandbox-contract-test) lets you re-verify with your
own credentials in minutes.

Not yet observed, so verify in your own rollout:

- A production call. Production additionally enforces the registered
  `Origin` domain and the IP whitelist; both are supported.
- An asynchronous payout reaching `COMPLETED` or `FAILED`; the sandbox
  left bank payouts at `ACCEPTED`, so the settle path is covered by tests
  against the documented responses only.
- A real callback delivery; `ParseCallback` is tested against the
  documented payload.
- Mobile wallet destinations; the sandbox's wallet sample was not accepted
  during validation.

## Contents

- [Installation](#installation)
- [Credentials](#credentials)
- [Quick start](#quick-start)
- [API](#api)
- [Reading results](#reading-results)
- [Errors](#errors)
- [Webhook](#webhook)
- [Sandbox](#sandbox)
- [Signing](#signing)
- [License](#license)

## Installation

```sh
go get github.com/Golang-Tanzania/selcom-business-client
```

```go
import business "github.com/Golang-Tanzania/selcom-business-client"
```

## Credentials

1. Log in to [Selcom Business](https://login.selcom.business) and open
   **My Account → API Credentials**.
2. Generate your **Signing Keys**. You receive an `api-key` and an RSA
   private key in PEM format. Download the key immediately and keep it on
   the server only; Selcom stores the matching public key.
3. Ask Selcom to whitelist the public IP addresses that will call the API.
   Requests from other addresses are rejected with error `611`.
4. Register the **domain** your service calls from against the credential.
   Every request must carry an `Origin` header matching it, or the gateway
   answers 403 with error `617`. Pass it with `WithOrigin`. The sandbox does
   not enforce this.
5. For the sandbox, request a separate `api-key` and private key from Selcom.

The debit account is resolved from the credentials, so there is no vendor id
or float PIN to configure.

## Quick start

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	business "github.com/Golang-Tanzania/selcom-business-client"
)

func main() {
	keyPEM, err := os.ReadFile(os.Getenv("SELCOM_BUSINESS_KEY_FILE"))
	if err != nil {
		log.Fatal(err)
	}

	cln, err := business.New(os.Getenv("SELCOM_BUSINESS_API_KEY"), keyPEM,
		business.WithBaseURL(business.SandboxBaseURL), // omit for production
	)
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	transID := fmt.Sprintf("PAY-%d", time.Now().UnixNano())

	// 1. Resolve the recipient's name and the charges for the amount.
	lk, err := cln.Lookup(ctx, business.LookupInput{
		Bank:          business.DestinationMPesa,
		Account:       "255712345678",
		TransactionID: transID,
		Amount:        10000,
	})
	if err != nil {
		log.Fatal(err)
	}

	// 2. Send the money.
	tr, err := cln.Transfer(ctx, business.TransferInput{
		TransactionID:    transID,
		RecipientFiCode:  business.DestinationMPesa,
		RecipientAccount: "255712345678",
		RecipientName:    lk.Data.AccountName,
		Amount:           10000,
		Purpose:          business.PurposePayment,
		Remarks:          "Invoice 1042",
	})
	if err != nil {
		log.Fatal(err)
	}

	// 3. Poll until the outcome is final. Never resubmit before then.
	for !tr.IsFinal() {
		time.Sleep(3 * time.Minute)

		st, err := cln.Status(ctx, transID)
		if err != nil {
			log.Fatal(err)
		}
		if st.IsFinal() {
			fmt.Println("final status:", st.Data.Status)
			break
		}
	}
}
```

A runnable version with per-call timeouts is in
[`examples/transfer`](examples/transfer/main.go).

## API

| Method | Endpoint | Purpose |
|--------|----------|---------|
| `Transfer(ctx, TransferInput)` | `POST /v1/transaction/process` | Send money to a bank, wallet or Selcom account |
| `Lookup(ctx, LookupInput)` | `GET /v1/account/lookup` | Resolve the account holder's name and charges |
| `Status(ctx, transID)` | `GET /v1/transaction/query` | Query a transaction by its `transId` |
| `Balance(ctx, accountNumber)` | `POST /v1/balance` | Available balance of an organisation account |
| `Statement(ctx, StatementInput)` | `POST /v1/statements` | Paginated listing or a PDF, XLSX or CSV export |
| `ParseCallback(r)` | — | Decode a success callback from Selcom |

Destination codes (`DestinationCRDB`, `DestinationMPesa`, ...) and purpose
codes (`PurposePayment`, `PurposeSalaryOrWages`, ...) are provided as
constants in [`codes.go`](codes.go). Any string is accepted, so institutions
added later can be used without a new release.

### Balance and statements

```go
bal, err := cln.Balance(ctx, "000011636")
fmt.Println(bal.Data.AvailableBalance)

stmt, err := cln.Statement(ctx, business.StatementInput{
	AccountNumber: "000011636",
	Preset:        business.PresetThisMonth, // or FromDate and ToDate as Y-m-d
	PerPage:       100,
})
for _, tx := range stmt.Data.Transactions {
	fmt.Println(tx)
}

export, err := cln.Statement(ctx, business.StatementInput{
	AccountNumber: "000011636",
	FromDate:      "2026-10-01",
	ToDate:        "2026-10-31",
	ExportType:    business.ExportPDF,
})
fmt.Println(export.Data.FileURL) // valid for one hour
```

### Client options

| Option | Effect |
|--------|--------|
| `WithOrigin(origin)` | Send the `Origin` header, e.g. `https://api.example.com`; must match the domain registered for the key (error 617). |
| `WithBaseURL(url)` | Target the sandbox or another host. Default is production. |
| `WithHTTPClient(hc)` | Use your own `*http.Client` for timeouts or proxies. |
| `WithLogger(fn)` | Receive request start and completion events. Bodies and credentials are never logged. |

## Reading results

Every successful response embeds an `Outcome` carrying Selcom's result codes:

| `resultcode` | Helper | Final |
|--------------|--------|-------|
| `000` | `IsSuccess()` | yes |
| `111`, `927` | `IsInProgress()` | no |
| `999` | `IsAmbiguous()` | no |
| anything else | `IsFailed()` | yes |

`data.status` on transfers and status queries is `ACCEPTED` (asynchronous,
keep polling), `COMPLETED` or `FAILED`.

Things to know:

- A transfer rejected at the bank or wallet can come back as HTTP 200 with
  `result: FAIL`. That is `err == nil`, so check the `Outcome` before using
  `Data`.
- **If a transfer is in progress or ambiguous, do not resubmit it**, with the
  same or a new `transId`. The money may already have moved. Poll `Status`
  until `IsFinal()` is true.
- Amounts are `int` whole shillings.
- The client never retries or polls on your behalf. `IsRetryable(err)`
  reports transport failures and 5xx answers; retrying a read is safe,
  retrying a `Transfer` is not until `Status` has been checked.

## Errors

Non-200 answers are returned as `*business.Error` with the HTTP status, the
gateway's `error_code`, message and data:

```go
var apiErr *business.Error
if errors.As(err, &apiErr) {
	switch {
	case apiErr.IsDuplicate():        // 643: already processed, fetch Status
	case apiErr.IsAuth():             // key, signature, timestamp or IP whitelist
	case apiErr.FieldErrors() != nil: // 651: map of field name to messages
	}
}
```

Requests that cannot be sent, such as an empty `transId`, an amount below 1
or remarks over 140 characters, fail locally with an error wrapping
`business.ErrInvalidInput`.

## Webhook

Selcom POSTs a callback to the URL configured in the portal when a transfer
**succeeds**. Failures are never called back; discover them with `Status`.

```go
http.HandleFunc("/selcom/callback", func(w http.ResponseWriter, r *http.Request) {
	cb, err := business.ParseCallback(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Callbacks carry no signature. Confirm before acting on one.
	st, err := cln.Status(r.Context(), cb.ReferenceID)
	if err == nil && st.IsSuccess() {
		markPaid(cb.ReferenceID)
	}

	w.WriteHeader(http.StatusOK)
})
```

## Sandbox contract test

`sandbox_test.go` (build tag `sandbox`) runs every endpoint against the real
sandbox with your credentials, prints each raw response and asserts the
decoding. The portal's response tables have proven unreliable about types,
so run it whenever Selcom changes something. The machine must be
IP-whitelisted for the credential; each run pays 1 TZS to the sandbox's
sample Selcom account.

```sh
SELCOM_SANDBOX_API_KEY=... SELCOM_SANDBOX_KEY_FILE=private.pem \
SELCOM_SANDBOX_ORIGIN=https://api.example.com SELCOM_SANDBOX_ACCOUNT=... \
go test -tags sandbox -run Sandbox -v .
```

## Sandbox

```go
cln, err := business.New(apiKey, keyPEM,
	business.WithOrigin("https://api.example.com"),
	business.WithBaseURL(business.SandboxBaseURL),
)
```

The portal lists the sandbox without a version path, but its endpoints live
under `/v1` like production; the client adds it. Sandbox header failures
use negative `error_code` values (for example `-11` for a missing header),
which `IsAuth()` recognises.

## Signing

Every request carries four headers: `api-key`, `timestamp` (ISO 8601 UTC
with milliseconds), `digest` and `signed-fields`. The digest is an
RSA-SHA256 (PKCS#1 v1.5) signature over

```
timestamp=<ts>&field1=value1&field2=value2...
```

in body order for POST and query order for GET. Values are signed exactly as
sent: numbers as their JSON literal, strings without quotes. PKCS#1 and
PKCS#8 private keys are accepted. The test suite verifies every request
against the public key the same way the gateway does.

## License

[MIT](LICENSE)
