// Package rss reads RSS 2.0 and Atom feeds into feed items: a title, a link,
// a plain-text summary and a publication time. Nothing else is read — the
// article body (content:encoded, a long Atom content) is used only when a feed
// gives no summary, and even then it is cut by the caller to a snippet.
package rss

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/foxcool/greedy-eye/internal/adapter/internal/urlerr"
	"github.com/foxcool/greedy-eye/internal/entity"
)

const (
	defaultTimeout  = 30 * time.Second
	defaultMaxBytes = 5 << 20
	userAgent       = "greedy-eye-news/1 (+https://github.com/foxcool/greedy-eye)"
)

// Config holds client configuration. Zero values use the defaults.
type Config struct {
	// Timeout bounds one fetch, body included.
	Timeout time.Duration
	// MaxBytes caps a feed body; a larger one is refused rather than cut,
	// since a cut XML document does not parse anyway.
	MaxBytes int64
}

// Client fetches and parses feeds.
type Client struct {
	httpClient *http.Client
	maxBytes   int64
}

func NewClient(cfg Config) *Client {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	maxBytes := cfg.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxBytes
	}
	return &Client{
		httpClient: &http.Client{Timeout: timeout},
		maxBytes:   maxBytes,
	}
}

// Fetch reads the feed at feedURL. Any non-2xx answer, an oversized body and
// a body that is not RSS or Atom are errors, so a broken feed is reported as
// broken instead of as a feed with no items.
func (c *Client) Fetch(ctx context.Context, feedURL string) ([]entity.FeedItem, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", urlerr.Strip(err))
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml;q=0.9, text/xml;q=0.8, */*;q=0.1")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// The URL may carry a token; the error is stored and shown.
		return nil, fmt.Errorf("fetch feed: %w", urlerr.Strip(err))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("feed answered %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read feed: %w", err)
	}
	if int64(len(body)) > c.maxBytes {
		return nil, fmt.Errorf("feed is larger than %d bytes", c.maxBytes)
	}
	return Parse(body)
}

// Parse reads an RSS 2.0 or Atom document. Items keep feed order; an item
// whose date does not parse has a zero PublishedAt.
func Parse(data []byte) ([]entity.FeedItem, error) {
	root, err := rootElement(data)
	if err != nil {
		return nil, err
	}
	switch {
	case root.Local == "rss":
		var doc rssDoc
		if err := decode(data, &doc); err != nil {
			return nil, err
		}
		return doc.items(), nil
	case root.Local == "feed" && root.Space == atomNS:
		var doc atomDoc
		if err := decode(data, &doc); err != nil {
			return nil, err
		}
		return doc.items(), nil
	default:
		return nil, fmt.Errorf("not an RSS or Atom feed: root element <%s>", root.Local)
	}
}

const (
	atomNS = "http://www.w3.org/2005/Atom"
	dcNS   = "http://purl.org/dc/elements/1.1/"
)

type rssDoc struct {
	Items []rssItem `xml:"channel>item"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	Content     string `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
	PubDate     string `xml:"pubDate"`
	DCDate      string `xml:"http://purl.org/dc/elements/1.1/ date"`
}

func (d rssDoc) items() []entity.FeedItem {
	out := make([]entity.FeedItem, 0, len(d.Items))
	for _, it := range d.Items {
		date := it.PubDate
		if strings.TrimSpace(date) == "" {
			date = it.DCDate
		}
		out = append(out, entity.FeedItem{
			Title:       PlainText(it.Title),
			Link:        strings.TrimSpace(it.Link),
			Summary:     summary(it.Description, it.Content),
			PublishedAt: parseTime(date),
		})
	}
	return out
}

type atomDoc struct {
	Entries []atomEntry `xml:"http://www.w3.org/2005/Atom entry"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
}

type atomEntry struct {
	Title     string     `xml:"http://www.w3.org/2005/Atom title"`
	Links     []atomLink `xml:"http://www.w3.org/2005/Atom link"`
	Summary   string     `xml:"http://www.w3.org/2005/Atom summary"`
	Content   string     `xml:"http://www.w3.org/2005/Atom content"`
	Published string     `xml:"http://www.w3.org/2005/Atom published"`
	Updated   string     `xml:"http://www.w3.org/2005/Atom updated"`
}

func (d atomDoc) items() []entity.FeedItem {
	out := make([]entity.FeedItem, 0, len(d.Entries))
	for _, e := range d.Entries {
		date := e.Published
		if strings.TrimSpace(date) == "" {
			date = e.Updated
		}
		out = append(out, entity.FeedItem{
			Title:       PlainText(e.Title),
			Link:        e.link(),
			Summary:     summary(e.Summary, e.Content),
			PublishedAt: parseTime(date),
		})
	}
	return out
}

// link picks the page a reader should open: the alternate HTML link, else
// the first alternate, else the first link of any kind.
func (e atomEntry) link() string {
	var alternate, first string
	for _, l := range e.Links {
		href := strings.TrimSpace(l.Href)
		if href == "" {
			continue
		}
		if first == "" {
			first = href
		}
		if l.Rel == "" || l.Rel == "alternate" {
			if l.Type == "" || strings.HasPrefix(l.Type, "text/html") {
				return href
			}
			if alternate == "" {
				alternate = href
			}
		}
	}
	if alternate != "" {
		return alternate
	}
	return first
}

func summary(short, long string) string {
	if s := PlainText(short); s != "" {
		return s
	}
	return PlainText(long)
}

var (
	scriptOrStyle = regexp.MustCompile(`(?is)<script\b.*?</script\s*>|<style\b.*?</style\s*>`)
	tag           = regexp.MustCompile(`(?s)<[^>]*>`)
)

// PlainText turns feed markup into one line of text: tags dropped, entities
// decoded, whitespace collapsed. Feeds put HTML in titles and summaries
// whether or not they declare it.
func PlainText(s string) string {
	s = scriptOrStyle.ReplaceAllString(s, " ")
	s = tag.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}

// timeLayouts are the date forms feeds use in practice: RFC 822 with and
// without seconds, weekday or numeric zone, and RFC 3339 for Atom and dc:date.
var timeLayouts = []string{
	time.RFC1123Z,
	time.RFC1123,
	"Mon, 2 Jan 2006 15:04:05 -0700",
	"Mon, 2 Jan 2006 15:04:05 MST",
	"Mon, 02 Jan 2006 15:04 -0700",
	"Mon, 02 Jan 2006 15:04 MST",
	"2 Jan 2006 15:04:05 -0700",
	"2 Jan 2006 15:04:05 MST",
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05Z0700",
	"2006-01-02 15:04:05 -0700",
}

func parseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// rootElement returns the name of the document's first element.
func rootElement(data []byte) (xml.Name, error) {
	dec := newDecoder(data)
	for {
		tok, err := dec.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return xml.Name{}, errors.New("not an RSS or Atom feed: no root element")
			}
			return xml.Name{}, fmt.Errorf("not an RSS or Atom feed: %w", err)
		}
		if start, ok := tok.(xml.StartElement); ok {
			return start.Name, nil
		}
	}
}

func decode(data []byte, v any) error {
	if err := newDecoder(data).Decode(v); err != nil {
		return fmt.Errorf("parse feed: %w", err)
	}
	return nil
}

// newDecoder is lenient about what feeds get wrong (undeclared HTML entities)
// and strict about what it cannot read: a charset other than UTF-8 is an
// error naming that charset, not mojibake in the headlines.
func newDecoder(data []byte) *xml.Decoder {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	dec.Entity = xml.HTMLEntity
	dec.CharsetReader = func(label string, input io.Reader) (io.Reader, error) {
		switch strings.ToLower(label) {
		case "utf-8", "utf8", "us-ascii", "ascii":
			return input, nil
		}
		return nil, fmt.Errorf("unsupported feed charset %q", label)
	}
	return dec
}
