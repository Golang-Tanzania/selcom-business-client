# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [1.2.0] - 2026-10-31

### Fixed

- Production sends `error_code` as a JSON string (the sandbox sends a
  number), which made every production response fail to decode with
  "cannot unmarshal string into ... error_code of type int". It is now
  decoded from a number, a numeric string or null; an unrecognised string
  decodes as 0 instead of failing the response.
- Decoding errors include the first 256 bytes of the response body, so an
  unexpected shape shows what the gateway actually said.

### Changed

- `Outcome.ErrorCode` is now of type `Code` (an `int`). Comparisons with
  constants are unaffected; code that assigns it to an `int` variable needs
  `int(resp.ErrorCode)`. `Error.ErrorCode` stays `int`.

## [1.1.0] - 2026-10-30

Validated against the Selcom sandbox; see the README's "Validation status"
for what was observed and what remains unverified.

### Added

- `WithOrigin` to send the `Origin` header the gateway requires, checked
  against the domain registered for the API credential (error 617 when
  missing or different; not enforced by the sandbox).
- `ErrCodeOriginNotRegistered` (617), classified by `IsAuth`.

- `sandbox_test.go` (build tag `sandbox`): a live contract test of every
  endpoint against the sandbox that prints raw responses and asserts the
  decoding.

- `TransferData.PrincipalAmount`, `TotalCharges`, `ChargesSummary`;
  `BalanceData.AccountName`; typed `StatementTransaction` (receipt in
  `ara_receipt`, string amounts), all observed live on the gateway.
- `Error.IsNotFound`; `IsDuplicate` also recognises the sandbox's `-34`.

### Fixed

- Monetary fields (`amount`, `totalCharges`, `available_balance`, statement
  balances, callback `amount`/`charges`) are a `Number` type that decodes
  either a JSON number or a numeric string; the sandbox sends strings on
  the status query where the portal documents numbers.
- Lookup decoding: the gateway returns `data.account` as an object
  describing the organisation's debit account (the portal documents a
  string) and `operator` may be null. `LookupData.Account` is now a
  `LookupAccount` that accepts both forms; the recipient's name remains in
  `AccountName`.

## [1.0.0] - 2026-10-27

### Added

- Initial client for the Selcom Business API Gateway v1: `Transfer`,
  `Lookup`, `Status`, `Balance`, `Statement`, `ParseCallback`.
- RSA-SHA256 request signing from a PKCS#1 or PKCS#8 private key, with
  values signed as their exact JSON literals in body order.
- `Outcome` helpers for the documented result codes, typed `*Error` with
  `IsAuth`, `IsDuplicate` and `FieldErrors`, destination and purpose code
  constants, sandbox support.
- Tests against an `httptest` gateway that verifies every signature with
  the public key.
