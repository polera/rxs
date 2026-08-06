// Package article downloads and extracts readable article-page content.
package article

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	readability "codeberg.org/readeck/go-readability/v2"
	"github.com/polera/rxs/internal/render"
)

const (
	maxResponseBytes    int64 = 5 << 20
	requestTimeout            = 15 * time.Second
	maxDocumentElements       = 100_000
)

var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001:2::/48"),
	netip.MustParsePrefix("2001:10::/28"),
	netip.MustParsePrefix("2001:db8::/32"),
}

// Content is a readability result ready to persist as an entry overlay.
type Content struct {
	Title     string
	HTML      string
	Text      string
	SourceURL string
}

// Extractor obtains static HTML and extracts its primary readable content.
type Extractor interface {
	Extract(context.Context, string) (Content, error)
}

type clientExtractor struct {
	http      *http.Client
	userAgent string
	validate  func(context.Context, *url.URL) error
}

// NewExtractor creates an extractor with bounded HTTP behavior and private
// network destinations disabled.
func NewExtractor(version string) Extractor {
	if version == "" {
		version = "dev"
	}
	validator := destinationValidator{resolver: net.DefaultResolver}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           safeDialer{resolver: net.DefaultResolver}.DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		IdleConnTimeout:       30 * time.Second,
	}
	return &clientExtractor{http: &http.Client{
		Timeout:   requestTimeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 5 {
				return errors.New("stopped after 5 redirects")
			}
			if err := validator.Validate(req.Context(), req.URL); err != nil {
				return fmt.Errorf("reject redirect destination: %w", err)
			}
			return nil
		},
	}, userAgent: "rxs/" + version + " (+https://github.com/polera/rxs)", validate: validator.Validate}
}

func (e *clientExtractor) Extract(ctx context.Context, rawURL string) (Content, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return Content{}, fmt.Errorf("parse article URL: %w", err)
	}
	if e.validate != nil {
		if err := e.validate(ctx, parsed); err != nil {
			return Content{}, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return Content{}, fmt.Errorf("create article request: %w", err)
	}
	userAgent := e.userAgent
	if userAgent == "" {
		userAgent = "rxs/dev (+https://github.com/polera/rxs)"
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html, application/xhtml+xml;q=0.9")
	response, err := e.http.Do(req)
	if err != nil {
		return Content{}, fmt.Errorf("fetch article: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Content{}, fmt.Errorf("fetch article: server returned %s", response.Status)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || (mediaType != "text/html" && mediaType != "application/xhtml+xml") {
		return Content{}, fmt.Errorf("article response is not HTML")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return Content{}, fmt.Errorf("read article: %w", err)
	}
	if int64(len(body)) > maxResponseBytes {
		return Content{}, fmt.Errorf("article exceeds %d MiB response limit", maxResponseBytes/(1<<20))
	}
	pageURL := response.Request.URL
	parser := readability.NewParser()
	parser.MaxElemsToParse = maxDocumentElements
	extracted, err := parser.Parse(bytes.NewReader(body), pageURL)
	if err != nil {
		return Content{}, fmt.Errorf("extract readable article: %w", err)
	}
	var html bytes.Buffer
	if err := extracted.RenderHTML(&html); err != nil {
		return Content{}, fmt.Errorf("render readable article: %w", err)
	}
	content := Content{
		Title:     strings.TrimSpace(extracted.Title()),
		HTML:      html.String(),
		SourceURL: pageURL.String(),
	}
	content.Text = render.Text(content.HTML)
	return content, nil
}

type destinationValidator struct {
	resolver interface {
		LookupIP(context.Context, string, string) ([]net.IP, error)
	}
}

func (v destinationValidator) Validate(ctx context.Context, target *url.URL) error {
	if target == nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") {
		return errors.New("article has no valid http or https URL")
	}
	if target.User != nil {
		return errors.New("article URL credentials are not allowed")
	}
	host := strings.TrimSuffix(strings.ToLower(target.Hostname()), ".")
	if host == "" {
		return errors.New("article URL has no host")
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || host == "metadata.google.internal" || host == "metadata.azure.internal" {
		return fmt.Errorf("article destination %q is not public", host)
	}
	ips, err := resolveHost(ctx, v.resolver, host)
	if err != nil {
		return fmt.Errorf("resolve article destination %q: %w", host, err)
	}
	for _, ip := range ips {
		if !publicIP(ip) {
			return fmt.Errorf("article destination %q is not public", host)
		}
	}
	return nil
}

type safeDialer struct {
	resolver interface {
		LookupIP(context.Context, string, string) ([]net.IP, error)
	}
}

func (d safeDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("parse destination address: %w", err)
	}
	ips, err := resolveHost(ctx, d.resolver, host)
	if err != nil {
		return nil, fmt.Errorf("resolve destination %q: %w", host, err)
	}
	for _, ip := range ips {
		if !publicIP(ip) {
			return nil, fmt.Errorf("destination %q is not public", host)
		}
	}
	var dialErrors []error
	var dialer net.Dialer
	for _, ip := range ips {
		connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return connection, nil
		}
		dialErrors = append(dialErrors, err)
	}
	return nil, fmt.Errorf("connect to %q: %w", host, errors.Join(dialErrors...))
}

func resolveHost(ctx context.Context, resolver interface {
	LookupIP(context.Context, string, string) ([]net.IP, error)
}, host string) ([]net.IP, error) {
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		return []net.IP{ip}, nil
	}
	ips, err := resolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, errors.New("host has no addresses")
	}
	return ips, nil
}

func publicIP(ip net.IP) bool {
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsMulticast() || address.IsUnspecified() {
		return false
	}
	for _, prefix := range nonPublicPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

// InputHash identifies the feed-owned input used for an enrichment attempt.
func InputHash(entryURL, feedHTML string, updatedAt time.Time) string {
	hash := sha256.New()
	for _, value := range []string{entryURL, feedHTML, updatedAt.UTC().Format(time.RFC3339Nano)} {
		_, _ = fmt.Fprintf(hash, "%d:", len(value))
		_, _ = io.WriteString(hash, value)
	}
	return hex.EncodeToString(hash.Sum(nil))
}
