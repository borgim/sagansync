package api

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
)

// Client talks to the daemon's Unix socket.
type Client struct{ hc *http.Client }

func NewClient(socket string) *Client {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", socket)
	}}
	return &Client{hc: &http.Client{Transport: tr}}
}

func (c *Client) Do(ctx context.Context, method, path string, q url.Values, body io.Reader) (*http.Response, error) {
	u := "http://sagand" + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	return c.hc.Do(req)
}
