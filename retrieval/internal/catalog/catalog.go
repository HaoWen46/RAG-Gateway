package catalog

// Catalog manages the document catalog and metadata.
type Catalog struct{}

func New() *Catalog {
	return &Catalog{}
}

// GetDocument retrieves document metadata by ID.
// TODO: Implement Postgres-backed catalog.
func (c *Catalog) GetDocument(id string) (*Document, error) {
	return nil, nil
}

type Document struct {
	ID        string
	Title     string
	TrustTier string
	Metadata  map[string]string
}
