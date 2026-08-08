package httpjson

import "net/http"

// Client is the minimal transport surface required by the JSON fetch helpers.
type Client interface {
	Do(*http.Request) (*http.Response, error)
}
