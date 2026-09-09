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
	"unicode/utf8"

	readability "codeberg.org/readeck/go-readability/v2"
	"github.com/polera/rxs/internal/render"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

const (
	maxResponseBytes    int64 = 5 << 20
	maxDecodedBytes     int64 = 5 << 20
	requestTimeout            = 15 * time.Second
	maxDocumentElements       = 100_000
	maxDocumentTags           = 100_000
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
	contentType := response.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || (mediaType != "text/html" && mediaType != "application/xhtml+xml") {
		return Content{}, fmt.Errorf("article response is not HTML")
	}
	body, err := io.ReadAll(io.LimitReader(contextReader{ctx, response.Body}, maxResponseBytes+1))
	if err != nil {
		return Content{}, fmt.Errorf("read article: %w", err)
	}
	if int64(len(body)) > maxResponseBytes {
		return Content{}, fmt.Errorf("article exceeds %d MiB response limit", maxResponseBytes/(1<<20))
	}
	return extractArticle(ctx, body, contentType, response.Request.URL)
}

func extractArticle(ctx context.Context, body []byte, contentType string, pageURL *url.URL) (Content, error) {
	body, err := decodeArticle(ctx, body, contentType)
	if err != nil {
		return Content{}, err
	}
	if err := preflightArticle(ctx, body); err != nil {
		return Content{}, err
	}
	doc, err := html.Parse(contextReader{ctx, bytes.NewReader(body)})
	if err != nil {
		return Content{}, fmt.Errorf("parse article DOM: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Content{}, err
	}
	parser := readability.NewParser()
	parser.MaxElemsToParse = maxDocumentElements
	// The DOM is request-local; mutation avoids a second DOM and decoder pass.
	// Readability has no context API. Stage checks do not impose a CPU deadline.
	extracted, err := parser.ParseAndMutate(doc, pageURL)
	if err != nil {
		return Content{}, fmt.Errorf("extract readable article: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Content{}, err
	}
	var output bytes.Buffer
	if err := extracted.RenderHTML(&output); err != nil {
		return Content{}, fmt.Errorf("render readable article: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Content{}, err
	}
	content := Content{
		Title:     strings.TrimSpace(extracted.Title()),
		HTML:      output.String(),
		SourceURL: pageURL.String(),
	}
	content.Text = render.Text(content.HTML)
	if err := ctx.Err(); err != nil {
		return Content{}, err
	}
	return content, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.r.Read(p)
	if ctxErr := r.ctx.Err(); ctxErr != nil {
		return n, ctxErr
	}
	return n, err
}

func decodeArticle(ctx context.Context, body []byte, contentType string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// The library only sniffs 1024 bytes. Preserve undeclared UTF-8 even when
	// non-ASCII text appears later in the already response-bounded buffer.
	// Meta declarations are also reported as uncertain, so check them separately.
	if _, name, certain := charset.DetermineEncoding(body, contentType); !certain && name == "windows-1252" && utf8.Valid(body) && !hasMetaCharset(body) {
		contentType = "text/html; charset=utf-8"
	}
	reader, err := charset.NewReader(contextReader{ctx, bytes.NewReader(body)}, contentType)
	if err != nil {
		return nil, fmt.Errorf("decode article charset: %w", err)
	}
	// Preserve go-shiori/dom's NFD -> literal soft-hyphen removal -> NFC policy.
	// Entity-encoded soft hyphens are still left for the HTML parser to decode.
	// Cap both decoder output and normalized input, so removal cannot hide expansion.
	decoded := &io.LimitedReader{R: reader, N: maxDecodedBytes + 1}
	normalized := transform.NewReader(decoded, transform.Chain(
		norm.NFD, runes.Remove(runes.Predicate(func(r rune) bool { return r == '\u00ad' })), norm.NFC,
	))
	body, err = io.ReadAll(io.LimitReader(contextReader{ctx, normalized}, maxDecodedBytes+1))
	if err != nil {
		return nil, fmt.Errorf("decode article: %w", err)
	}
	if decoded.N == 0 || int64(len(body)) > maxDecodedBytes {
		return nil, fmt.Errorf("article exceeds %d MiB decoded-input limit", maxDecodedBytes/(1<<20))
	}
	return body, nil
}

// hasMetaCharset follows charset.DetermineEncoding's 1024-byte meta prescan,
// including first-attribute wins, attribute ordering, and the http-equiv pragma.
// Its API does not distinguish a meta declaration from the Windows-1252 fallback.
func hasMetaCharset(body []byte) bool {
	z := html.NewTokenizer(bytes.NewReader(body[:min(len(body), 1024)]))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return false
		case html.StartTagToken, html.SelfClosingTagToken:
			token := z.Token()
			if token.Data != "meta" {
				continue
			}
			seen := make(map[string]bool)
			recognized, needsPragma, pragma := false, false, false
			for _, attr := range token.Attr {
				if seen[attr.Key] {
					continue
				}
				seen[attr.Key] = true
				switch attr.Key {
				case "http-equiv":
					pragma = strings.EqualFold(attr.Val, "content-type")
				case "charset":
					encoding, _ := charset.Lookup(attr.Val)
					recognized, needsPragma = encoding != nil, false
				case "content":
					if !recognized {
						encoding, _ := charset.Lookup(metaContentCharset(strings.ToLower(attr.Val)))
						if encoding != nil {
							recognized, needsPragma = true, true
						}
					}
				}
			}
			if recognized && (!needsPragma || pragma) {
				return true
			}
		}
	}
}

func metaContentCharset(value string) string {
	const space = " \t\n\f\r"
	for {
		_, rest, found := strings.Cut(value, "charset")
		if !found {
			return ""
		}
		value = strings.TrimLeft(rest, space)
		if !strings.HasPrefix(value, "=") {
			continue
		}
		value = strings.TrimLeft(value[1:], space)
		if value == "" {
			return ""
		}
		if quote := value[0]; quote == '\'' || quote == '"' {
			if end := strings.IndexByte(value[1:], quote); end >= 0 {
				return value[1 : end+1]
			}
			return ""
		}
		if end := strings.IndexAny(value, space+";"); end >= 0 {
			return value[:end]
		}
		return value
	}
}

func preflightArticle(ctx context.Context, body []byte) error {
	// Count every literal '<', including comments, attributes, and script text.
	// A standalone tokenizer cannot mirror tree-builder states: comments or quotes
	// inside scripts can otherwise swallow real tags. This intentionally rejects
	// some low-element pages. Keep readability's separate DOM element limit too.
	tags := 0
	for len(body) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := min(len(body), 16<<10)
		tags += bytes.Count(body[:n], []byte{'<'})
		if tags > maxDocumentTags {
			return fmt.Errorf("article exceeds %d tag preflight limit", maxDocumentTags)
		}
		body = body[n:]
	}
	return ctx.Err()
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
	dial func(context.Context, string, string) (net.Conn, error)
}

func (d safeDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	// Transport may detach the request deadline from its dial context. Bound DNS
	// and all attempts together even when the caller supplies no deadline.
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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
	dial := d.dial
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	deadline, _ := ctx.Deadline()
	for i, ip := range ips {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		attempt, cancelAttempt := context.WithTimeout(ctx, time.Until(deadline)/time.Duration(len(ips)-i))
		connection, err := dial(attempt, network, net.JoinHostPort(ip.String(), port))
		cancelAttempt()
		if ctxErr := ctx.Err(); ctxErr != nil {
			if connection != nil {
				_ = connection.Close()
			}
			return nil, ctxErr
		}
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
