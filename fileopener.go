package push

import (
	"io"
	"os"
	"path"
)

// FileOpener is an interface that allows the parser to load embedded resources recursively.
type FileOpener interface {
	Open(string) (io.Reader, string, error)
}

type FileOpenerFunc func(string) (io.Reader, string, error)

func (f FileOpenerFunc) Open(uri string) (io.Reader, string, error) {
	return f(uri)
}

////////////////

type DefaultFileOpener struct {
	basePath string
}

func NewDefaultFileOpener(basePath string) *DefaultFileOpener {
	return &DefaultFileOpener{basePath}
}

func (o *DefaultFileOpener) Open(uri string) (io.Reader, string, error) {
	r, err := os.Open(path.Join(o.basePath, uri))
	if err != nil {
		return nil, "", err
	}
	return r, ExtToMimetype[path.Ext(uri)], nil
}
