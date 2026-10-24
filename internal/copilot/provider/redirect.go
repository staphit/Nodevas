package provider

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// sameHostRedirects is an http.Client's CheckRedirect for a provider: a
// redirect is followed only to the host (and scheme) the request went to.
// Go drops Authorization on a redirect to another host, but not x-api-key or
// api-key, so an endpoint (or whoever answers for it) that redirects
// elsewhere would otherwise receive the key there.
func sameHostRedirects(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	first := via[0].URL
	if !strings.EqualFold(req.URL.Host, first.Host) || req.URL.Scheme != first.Scheme {
		return fmt.Errorf("refused a redirect from %s://%s to %s://%s: the API key goes only to the configured endpoint",
			first.Scheme, first.Host, req.URL.Scheme, req.URL.Host)
	}
	return nil
}

// anthropicHTTPClient is the SDK's default client (its transport, with a
// ten-minute wait for response headers) that also refuses cross-host
// redirects.
func anthropicHTTPClient() *http.Client {
	client := &http.Client{CheckRedirect: sameHostRedirects}
	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		t = t.Clone()
		t.ResponseHeaderTimeout = 10 * time.Minute
		client.Transport = t
	}
	return client
}
