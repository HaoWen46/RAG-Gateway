package cache

// Cache provides Redis-backed caching for retrieval results.
type Cache struct {
	addr string
}

func New(addr string) *Cache {
	return &Cache{addr: addr}
}

// Get retrieves a cached result.
// TODO: Implement Redis client.
func (c *Cache) Get(key string) (string, error) {
	return "", nil
}

// Set stores a result in the cache.
// TODO: Implement Redis client.
func (c *Cache) Set(key, value string) error {
	return nil
}
