# Selcom Business Go Client

Go client for the [Selcom Business API Gateway v1](https://developer.selcom.business):
outbound transfers to banks, mobile wallets and Selcom accounts, recipient
lookup, transaction status, account balance and statements.

This is a different product from the legacy Selcom Pay gateway
(`apigw.selcommobile.com`) that
[`selcompay-client`](https://github.com/Golang-Tanzania/selcompay-client)
covers. The Business gateway uses RSA-SHA256 request signing with a private
key, one transfer endpoint for every destination, and the debit account is
resolved from the API credentials, so there is no vendor id or float PIN. It
does **not** cover checkout or collections; keep using `selcompay-client` for
those.

Standard library only. Go 1.23 or later.

## Installation

```sh
go get github.com/Golang-Tanzania/selcom-business-client
```

## Credentials

In Selcom Business go to *My Account → API Credentials* and generate your
signing keys. You get an `api-key` and an RSA private key (PEM). Keep the
private key on the server only. Selcom whitelists the IP addresses that may
use the key.

## Usage

```go
import business "github.com/Golang-Tanzania/selcom-business-client"

keyPEM, _ := os.ReadFile("private_key.pem")

cln, err := business.New(apiKey, keyPEM,
    business.WithBaseURL(business.SandboxBaseURL), // omit for production
)

// Resolve the recipient's name (and the charges for an amount) first.
lk, err := cln.Lookup(ctx, business.LookupInput{
    Bank: business.DestinationCRDB, Account: "0150123456789", TransactionID: transID, Amount: 50000,
})

// Pay. transId must be unique; a recent repeat is rejected with error 643.
tr, err := cln.Transfer(ctx, business.TransferInput{
    TransactionID:    transID,
    RecipientFiCode:  business.DestinationCRDB,
    RecipientAccount: "0150123456789",
    RecipientName:    lk.Data.AccountName,
    Amount:           50000,
    Purpose:          business.PurposeSalaryOrWages,
    Remarks:          "October payroll",
})

// Poll until final. Never resubmit an in-progress or ambiguous transfer.
final := tr.IsFinal()
for !final {
    time.Sleep(3 * time.Minute)
    st, err := cln.Status(ctx, transID)
    if err != nil {
        return err
    }
    final = st.IsFinal()
}

bal, err := cln.Balance(ctx, "000011636")
fmt.Println(bal.Data.AvailableBalance)

stmt, err := cln.Statement(ctx, business.StatementInput{
    AccountNumber: "000011636", Preset: business.PresetThisMonth, PerPage: 100,
})
```

## Outcomes

Every successful response embeds `Outcome` with Selcom's result codes:

| `resultcode`   | helper           | final |
|----------------|------------------|-------|
| `000`          | `IsSuccess()`    | yes   |
| `111`, `927`   | `IsInProgress()` | no    |
| `999`          | `IsAmbiguous()`  | no    |
| anything else  | `IsFailed()`     | yes   |

`data.status` on transfers and status queries is `ACCEPTED` (asynchronous,
poll), `COMPLETED` or `FAILED`. A transfer the gateway rejects at the bank
or wallet can come back as HTTP 200 with `result: FAIL`; that is `err == nil`,
so always read the `Outcome` before using `Data`. Amounts are `int` whole
shillings. The client never retries or polls for you.
**If a transfer is in progress or ambiguous, do not resubmit it** with the
same or a new `transId`; the money may already have moved. Poll `Status`
until `IsFinal()` is true.

## Errors

Non-200 answers are returned as `*business.Error` with the HTTP status, the
gateway's `error_code`, message and data:

```go
var apiErr *business.Error
if errors.As(err, &apiErr) {
    switch {
    case apiErr.IsDuplicate():      // 643: already processed, fetch Status
    case apiErr.IsAuth():           // key, signature, timestamp or IP whitelist
    case apiErr.FieldErrors() != nil: // 651: map of field to messages
    }
}
```

Requests that cannot be sent (empty `transId`, amount below 1, remarks over
140 characters, ...) fail locally with an error wrapping
`business.ErrInvalidInput`.

## Webhook

Selcom POSTs a callback to the URL configured in the portal when a transfer
**succeeds**; failures are never called back. `ParseCallback(r)` decodes it.
The portal documents no signature on callbacks, so confirm with `Status`
before acting on one.

## Signing

Requests carry `api-key`, `timestamp` (ISO 8601 UTC with milliseconds),
`digest` and `signed-fields`. The digest is an RSA-SHA256 (PKCS#1 v1.5)
signature over `timestamp=<ts>&field1=value1&...` in body (or query) order.
Values are signed exactly as sent: numbers as their JSON literal, strings
without quotes. The test suite verifies every request against the public
key the same way the gateway does.

## Sandbox

`WithBaseURL(business.SandboxBaseURL)`. The portal lists the sandbox without
a version path, but its endpoints are under `/v1` like production; the
client adds it. Sandbox header failures use negative `error_code` values
(for example `-11`), which `IsAuth()` recognises.

## License

[MIT](LICENSE)
