# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- `WithOrigin` to send the `Origin` header the gateway requires, checked
  against the domain registered for the API credential (error 617 when
  missing or different; not enforced by the sandbox).
- `ErrCodeOriginNotRegistered` (617), classified by `IsAuth`.

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
