package entity

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"
	"time"
)

// NewsSourceKind is how a source is read. RSS covers Atom too: one reader
// handles both.
type NewsSourceKind string

const NewsSourceRSS NewsSourceKind = "rss"

// Length limits a stored document is cut to. The database checks the same
// numbers: a document is a headline and a pointer, never the article.
const (
	MaxDocumentTitleRunes   = 300
	MaxDocumentSnippetRunes = 300
	MaxDocumentURLBytes     = 2048
)

// NewsSource is one feed the news job reads, with the outcome of its last
// fetch: a feed that fails must read as failing, not as one with no news.
type NewsSource struct {
	ID            string
	Kind          NewsSourceKind
	URL           string
	Name          string
	Enabled       bool
	Cron          string
	Weight        int32
	LastFetchedAt *time.Time
	LastSuccessAt *time.Time
	LastError     string
	LastErrorAt   *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// NewsDocument is one headline. PublishedAt is the time it is shown with;
// FetchedAt is bookkeeping and never reaches a reader.
type NewsDocument struct {
	ID          string
	SourceID    string
	SourceName  string // read-side only: joined from the source
	URL         string
	Title       string
	Snippet     string
	PublishedAt time.Time
	FetchedAt   time.Time
	URLHash     string
}

// FeedItem is one entry as a feed reader returned it, before it is cut to a
// document. PublishedAt is zero when the feed gave no date it could parse.
type FeedItem struct {
	Title       string
	Link        string
	Summary     string
	PublishedAt time.Time
}

// trackingParams are query parameters that name who shared a link, not what
// it points to; two links differing only in them are the same article.
var trackingParams = []string{"fbclid", "gclid", "mc_cid", "mc_eid"}

// CanonicalURL reduces a link to the form two copies of one article share:
// lower-case scheme and host, no default port, no fragment, no tracking
// parameters, remaining parameters sorted. Only http and https are accepted.
func CanonicalURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("url scheme %q is not http or https", u.Scheme)
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", errors.New("url has no host")
	}
	port := u.Port()
	if (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		port = ""
	}
	switch {
	case port != "":
		u.Host = net.JoinHostPort(host, port)
	case strings.Contains(host, ":"): // IPv6 literal
		u.Host = "[" + host + "]"
	default:
		u.Host = host
	}
	u.User = nil
	u.Fragment, u.RawFragment = "", ""
	if u.Path == "" {
		u.Path = "/"
	}
	q := u.Query()
	for key := range q {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "utm_") || slices.Contains(trackingParams, lower) {
			q.Del(key)
		}
	}
	u.RawQuery = q.Encode() // Encode sorts by key
	u.ForceQuery = false
	return u.String(), nil
}

// URLHash is the dedup key of a document: sha256 of its canonical URL.
func URLHash(raw string) (string, error) {
	canonical, err := CanonicalURL(raw)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:]), nil
}

// TruncateRunes cuts s to at most n runes, marking a cut with an ellipsis
// that counts toward n.
func TruncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}
