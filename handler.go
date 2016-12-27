package push

import (
	"errors"
	"net/http"
	"sync"
)

// ErrNoPusher is returned when the ResponseWriter does not implement the Pusher interface.
var ErrNoPusher = errors.New("ResponseWriter is not a Pusher")

// URIHandler is a callback definition that is called when a resource URI is found.
type URIHandler interface {
	URI(string) error
}

type URIHandlerFunc func(string) error

func (f URIHandlerFunc) URI(uri string) error {
	return f(uri)
}

////////////////

// PushHandler is a URIHandler that pushes resources to the client.
type PushHandler struct {
	pusher http.Pusher
	opts   *http.PushOptions
}

func NewPushHandler(pusher http.Pusher, opts *http.PushOptions) *PushHandler {
	if opts == nil {
		opts = &http.PushOptions{"", http.Header{}}
	}
	opts.Header.Set("X-Pushed", "1")
	return &PushHandler{pusher, opts}
}

func NewPushHandlerFromResponseWriter(w http.ResponseWriter) (*PushHandler, error) {
	pusher, ok := w.(http.Pusher)
	if !ok {
		return nil, ErrNoPusher
	}
	opts := &http.PushOptions{"", http.Header{}}
	opts.Header.Set("X-Pushed", "1")
	return &PushHandler{pusher, opts}, nil
}

func (p *PushHandler) URI(uri string) error {
	return p.pusher.Push(uri, p.opts)
}

////////////////

// ListHandler is a URIHandler that collects all resource URIs in a list.
type ListHandler struct {
	URIs  []string
	mutex sync.Mutex
}

func NewListHandler() *ListHandler {
	return &ListHandler{[]string{}, sync.Mutex{}}
}

func (h *ListHandler) URI(uri string) error {
	h.mutex.Lock()
	h.URIs = append(h.URIs, uri)
	h.mutex.Unlock()
	return nil
}
