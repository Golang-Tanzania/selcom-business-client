// Package business is a Go client for the Selcom Business API Gateway v1
// (https://developer.selcom.business): outbound transfers to banks, mobile
// wallets and Selcom accounts, recipient lookup, transaction status, account
// balance and statements.
//
// Requests are signed with RSA-SHA256 using the private key issued in the
// Selcom Business portal. The gateway verifies the signature with the stored
// public key; the private key never leaves your server.
//
//	cln, err := business.New(apiKey, privateKeyPEM)
//	resp, err := cln.Transfer(ctx, business.TransferInput{
//	    TransactionID:    "TXN-0001",
//	    RecipientFiCode:  business.DestinationMPesa,
//	    RecipientAccount: "255712345678",
//	    RecipientName:    "Jane Smith",
//	    Amount:           10000,
//	    Purpose:          business.PurposePayment,
//	})
package business

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// ProductionBaseURL is the live gateway.
	ProductionBaseURL = "https://api.selcom.business"

	// SandboxBaseURL is the test gateway. The portal lists it without a
	// version path but the endpoints live under /v1 there as well.
	SandboxBaseURL = "https://sandbox.selcom.business"

	version = "v1"

	// timestampLayout is ISO 8601 UTC with millisecond precision, as the
	// gateway requires: 2026-05-27T06:01:03.273Z.
	timestampLayout = "2006-01-02T15:04:05.000Z"

	// maxErrorBody bounds how much of an unparseable response is quoted in an
	// error. Kept small because callers log errors.
	maxErrorBody = 256
)

// Logger is called with request lifecycle events. Nil disables logging.
type Logger func(ctx context.Context, msg string, args ...any)

// Client talks to the Selcom Business API Gateway.
type Client struct {
	baseURL    string
	apiKey     string
	origin     string
	privateKey *rsa.PrivateKey
	http       *http.Client
	log        Logger
	now        func() time.Time
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL selects the gateway, e.g. SandboxBaseURL. The default is
// ProductionBaseURL. A trailing /v1 is tolerated and stripped.
func WithBaseURL(u string) Option {
	return func(c *Client) {
		u = strings.TrimRight(strings.TrimSpace(u), "/")
		u = strings.TrimSuffix(u, "/"+version)
		if u != "" {
			c.baseURL = u
		}
	}
}

// WithOrigin sets the Origin header sent with every request. The gateway
// requires it and checks it against the domains registered for the API
// credential in the portal (error 617 when missing or different), e.g.
// "https://api.example.com". The sandbox does not enforce it.
func WithOrigin(origin string) Option {
	return func(c *Client) { c.origin = strings.TrimSpace(origin) }
}

// WithHTTPClient replaces the default HTTP client, e.g. to set a different
// timeout or route through a proxy.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		if hc != nil {
			c.http = hc
		}
	}
}

// WithLogger adds a logger for request start/complete events. Bodies and
// credentials are never logged.
func WithLogger(l Logger) Option {
	return func(c *Client) { c.log = l }
}

// withClock overrides the time source. Used by tests.
func withClock(now func() time.Time) Option {
	return func(c *Client) { c.now = now }
}

// New constructs a client from the api-key and the PEM encoded RSA private
// key downloaded from the Selcom Business credentials panel. PKCS#1 and
// PKCS#8 encodings are accepted.
func New(apiKey string, privateKeyPEM []byte, opts ...Option) (*Client, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("%w: api key is required", ErrInvalidInput)
	}

	key, err := parsePrivateKey(privateKeyPEM)
	if err != nil {
		return nil, err
	}

	c := &Client{
		baseURL:    ProductionBaseURL,
		apiKey:     apiKey,
		privateKey: key,
		http: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
				DialContext: (&net.Dialer{
					Timeout:   10 * time.Second,
					KeepAlive: 15 * time.Second,
				}).DialContext,
				ForceAttemptHTTP2:     true,
				MaxIdleConns:          100,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
			},
		},
		now: time.Now,
	}

	for _, o := range opts {
		o(c)
	}

	return c, nil
}

// BaseURL reports the gateway the client sends requests to.
func (c *Client) BaseURL() string {
	return c.baseURL
}

func parsePrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(bytes.TrimSpace(pemBytes))
	if block == nil {
		return nil, fmt.Errorf("%w: private key is not PEM encoded", ErrInvalidInput)
	}

	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}

	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%w: parsing private key: %v", ErrInvalidInput, err)
	}

	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%w: private key is %T, want RSA", ErrInvalidInput, parsed)
	}

	return key, nil
}

// post sends a signed JSON POST. body must be a struct whose field order is
// the Signed-Fields order; v receives the decoded envelope.
func (c *Client) post(ctx context.Context, path string, body any, v any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("client: encoding request body: %w", err)
	}

	fields, err := fieldsFromJSON(data)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/"+version+path, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("client: creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	return c.do(req, fields, v)
}

// get sends a signed GET whose query parameters are the signed fields, in
// the order given.
func (c *Client) get(ctx context.Context, path string, params []field, v any) error {
	q := url.Values{}
	for _, f := range params {
		q.Set(f.key, f.value)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/"+version+path+"?"+q.Encode(), nil)
	if err != nil {
		return fmt.Errorf("client: creating request: %w", err)
	}

	return c.do(req, params, v)
}

func (c *Client) do(req *http.Request, fields []field, v any) error {
	ctx := req.Context()

	timestamp := c.now().UTC().Format(timestampLayout)

	digest, err := c.sign(timestamp, fields)
	if err != nil {
		return err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("api-key", c.apiKey)
	if c.origin != "" {
		req.Header.Set("Origin", c.origin)
	}
	req.Header.Set("timestamp", timestamp)
	req.Header.Set("digest", digest)
	req.Header.Set("signed-fields", signedFields(fields))

	c.logf(ctx, "request started", "method", req.Method, "path", req.URL.Path)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("client: %s %s: %w", req.Method, req.URL.Path, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("client: reading response: %w", err)
	}

	c.logf(ctx, "request completed", "method", req.Method, "path", req.URL.Path, "status", resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		return decodeError(resp.StatusCode, data)
	}

	return decodeResponse(data, v)
}

// decodeResponse decodes a 200 envelope into the typed response. The gateway
// sends data as an empty array when there is no payload (for example a 200
// carrying result FAIL), which would not fit the typed Data structs, so a
// non-object data member is replaced by an empty object first. Every other
// top-level member is preserved.
func decodeResponse(data []byte, v any) error {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil {
		return fmt.Errorf("client: decoding response: %w", err)
	}

	if d := bytes.TrimSpace(members["data"]); len(d) == 0 || d[0] != '{' {
		members["data"] = json.RawMessage("{}")

		var err error
		if data, err = json.Marshal(members); err != nil {
			return fmt.Errorf("client: decoding response: %w", err)
		}
	}

	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("client: decoding response: %w", err)
	}

	return nil
}

func (c *Client) logf(ctx context.Context, msg string, args ...any) {
	if c.log != nil {
		c.log(ctx, msg, args...)
	}
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "..."
	}

	return string(b)
}

// decodeError turns a non-200 response into an *Error. The gateway always
// answers with its JSON envelope; anything else is reported verbatim.
func decodeError(status int, data []byte) error {
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil || (env.Message == "" && env.ErrorCode == 0) {
		return &Error{
			HTTPStatus: status,
			Message:    fmt.Sprintf("unexpected response: %s", truncate(data, maxErrorBody)),
		}
	}

	return &Error{
		HTTPStatus: status,
		ErrorCode:  env.ErrorCode,
		Message:    env.Message,
		Result:     env.Result,
		ResultCode: env.ResultCode,
		Data:       env.Data,
	}
}

// IsRetryable reports whether an error came from a transport failure or a
// 5xx answer. A cancelled or expired context is not retryable. For reads
// (Lookup, Status, Balance, Statement) a retry is safe; for Transfer the
// request may have reached the gateway, so check Status for the transId
// before sending it again.
func IsRetryable(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	var apiErr *Error
	if errors.As(err, &apiErr) {
		return apiErr.HTTPStatus >= 500
	}

	var netErr net.Error

	return errors.As(err, &netErr)
}
