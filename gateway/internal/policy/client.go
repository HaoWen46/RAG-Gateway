package policy

// Client communicates with OPA for policy decisions.
type Client struct {
	endpoint string
}

func NewClient(endpoint string) *Client {
	return &Client{endpoint: endpoint}
}

// CheckRetrieval evaluates whether a retrieval request is allowed.
// TODO: Implement OPA REST API call.
func (c *Client) CheckRetrieval(userRole string, docIDs []string) (bool, error) {
	return true, nil
}

// CheckCompile evaluates whether a compile-to-LoRA request is allowed.
// TODO: Implement OPA REST API call.
func (c *Client) CheckCompile(userRole string, sectionIDs []string) (bool, error) {
	return true, nil
}
