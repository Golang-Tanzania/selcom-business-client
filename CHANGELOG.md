# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- `WithOrigin` to send the `Origin` header the gateway requires, checked
  against the domain registered for the API credential (error 617 when
  missing or different; not enforced by the sandbox).
- `ErrCodeOriginNotRegistered` (617), classified by `IsAuth`.

- `sandbox_test.go` (build tag `sandbox`): a live contract test of every
  endpoint against the sandbox that prints raw responses and asserts the
  decoding.

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
