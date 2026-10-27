package business

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// field is one signed request field in signing order. value is the exact
// text that appears in the request: for JSON bodies the JSON literal (so
// 100 stays "100" and "01234" keeps its leading zero), for GET requests the
// query parameter value.
type field struct {
	key   string
	value string
}

// signingString builds "timestamp=<ts>&k1=v1&k2=v2..." exactly as the
// gateway reconstructs it. timestamp is always first and is not listed in
// signed-fields.
func signingString(timestamp string, fields []field) string {
	var b strings.Builder

	b.WriteString("timestamp=")
	b.WriteString(timestamp)

	for _, f := range fields {
		b.WriteByte('&')
		b.WriteString(f.key)
		b.WriteByte('=')
		b.WriteString(f.value)
	}

	return b.String()
}

func signedFields(fields []field) string {
	keys := make([]string, len(fields))
	for i, f := range fields {
		keys[i] = f.key
	}

	return strings.Join(keys, ",")
}

// sign produces the base64 RSA-SHA256 (PKCS#1 v1.5) digest of the signing
// string, matching the portal's PHP (openssl_sign), Python (PKCS1v15) and
// Node (RSA-SHA256) examples.
func (c *Client) sign(timestamp string, fields []field) (string, error) {
	sum := sha256.Sum256([]byte(signingString(timestamp, fields)))

	sig, err := rsa.SignPKCS1v15(rand.Reader, c.privateKey, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("client: signing request: %w", err)
	}

	return base64.StdEncoding.EncodeToString(sig), nil
}

// fieldsFromJSON extracts the top-level members of an encoded JSON object
// in document order, keeping each value as its exact literal. Strings are
// unquoted (the gateway signs the raw text, not the JSON encoding); numbers,
// booleans and null are used verbatim. Nested objects and arrays are not
// supported because the gateway does not define how they are signed.
func fieldsFromJSON(data []byte) ([]field, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("client: request body: %w", err)
	}

	if tok != json.Delim('{') {
		return nil, fmt.Errorf("client: request body must be a JSON object")
	}

	var fields []field

	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("client: request body: %w", err)
		}

		key, ok := keyTok.(string)
		if !ok {
			return nil, fmt.Errorf("client: request body: unexpected token %v", keyTok)
		}

		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, fmt.Errorf("client: request body: %w", err)
		}

		value, err := literal(raw)
		if err != nil {
			return nil, fmt.Errorf("client: request body: field %q: %w", key, err)
		}

		fields = append(fields, field{key: key, value: value})
	}

	return fields, nil
}

func literal(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "", fmt.Errorf("empty value")
	}

	switch raw[0] {
	case '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", err
		}
		return s, nil

	case '{', '[':
		return "", fmt.Errorf("nested values cannot be signed")

	default:
		return string(raw), nil
	}
}
