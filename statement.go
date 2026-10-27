package business

import "context"

// Statement returns a paginated statement of an account, or a temporary
// download link when ExportType is pdf, xlsx or csv (POST /v1/statements).
func (c *Client) Statement(ctx context.Context, in StatementInput) (StatementResponse, error) {
	if err := in.validate(); err != nil {
		return StatementResponse{}, err
	}

	var resp StatementResponse
	if err := c.post(ctx, "/statements", in, &resp); err != nil {
		return StatementResponse{}, err
	}

	return resp, nil
}
