package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// ---- fetching what people type in ----
//
// Stations, playlists and logos are fetched by the server from addresses a
// person typed or a directory listed. Without a guard that would let anyone
// with an account read the box's own network through the relay (Core on
// localhost, a router page). So by default every outbound fetch refuses
// loopback, private, link-local, CGNAT (Tailscale) and multicast addresses —
// checked on the address actually dialled, after DNS and on every redirect.
// ALLOW_LOCAL_STREAMS=true lifts it for an Icecast at home.

var errLocalAddress = errors.New("this address is on a private network; the box only plays stations from the internet (ALLOW_LOCAL_STREAMS=true allows local ones)")

var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

func blockedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() ||
		ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() || cgnat.Contains(ip)
}

// outClient is an HTTP client for outside addresses. timeout 0 = none (a
// stream plays for hours); dialling and the response headers are still
// bounded.
func (s *Server) outClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	if !s.cfg.LocalStreams {
		dialer.Control = func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip == nil || blockedIP(ip) {
				return errLocalAddress
			}
			return nil
		}
	}
	tr := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		IdleConnTimeout:       60 * time.Second,
		MaxIdleConns:          20,
		ForceAttemptHTTP2:     true,
	}
	return &http.Client{Timeout: timeout, Transport: tr, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 6 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return fmt.Errorf("redirect to %s is not allowed", req.URL.Scheme)
		}
		return nil
	}}
}

// isLocalErr reports whether a fetch failed on the guard.
func isLocalErr(err error) bool { return errors.Is(err, errLocalAddress) }

// outGet starts a GET to an outside address with the app's user agent.
func (s *Server) outGet(ctx context.Context, c *http.Client, url string, hdr map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", s.cfg.UserAgent)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return c.Do(req)
}
