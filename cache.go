package push

import "sync"

// Cache is an interface that allows Middleware and ResponseWriter to cache the results of the list of resources to improve performance.
type Cache interface {
	Get(string) ([]string, bool)
	Add(string, string)
	Del(string)
}

////////////////

type DefaultCache struct {
	uris  map[string][]string
	mutex sync.RWMutex
}

func NewDefaultCache() *DefaultCache {
	return &DefaultCache{make(map[string][]string), sync.RWMutex{}}
}

func (c *DefaultCache) Get(uri string) ([]string, bool) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	resources, ok := c.uris[uri]
	return resources, ok
}

func (c *DefaultCache) Add(uri string, resource string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if _, ok := c.uris[uri]; !ok {
		c.uris[uri] = []string{resource}
		return
	}
	c.uris[uri] = append(c.uris[uri], resource)
}

func (c *DefaultCache) Del(uri string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	delete(c.uris, uri)
}
