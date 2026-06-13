// Package aclanthology is the library behind the acl command line:
// the HTTP client, request shaping, and typed data models for the ACL Anthology.
//
// Data sources:
//   - ACL Anthology (aclanthology.org) — volume listings, individual paper pages,
//     events index. All are static HTML; no JavaScript required.
//   - Semantic Scholar public API (api.semanticscholar.org/graph/v1) — paper
//     search with no API key at 1 request/second. Results are filtered to
//     papers that carry an ACL Anthology external ID so every search hit links
//     back to aclanthology.org.
package aclanthology

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// BaseURL is the ACL Anthology root.
const BaseURL = "https://aclanthology.org"

// s2BaseURL is the Semantic Scholar Graph API root.
const s2BaseURL = "https://api.semanticscholar.org/graph/v1"

// DefaultUserAgent identifies the client.
const DefaultUserAgent = "acl/dev (+https://github.com/tamnd/aclanthology-cli)"

// ErrNotFound is returned when a paper or volume ID does not exist.
var ErrNotFound = errors.New("not found")

// Config holds constructor parameters.
type Config struct {
	// BaseURL is the ACL Anthology root. Override in tests to point at a mock server.
	BaseURL   string
	UserAgent string
	// Rate is the minimum spacing between requests. Zero means no pacing.
	Rate    time.Duration
	Retries int
	Timeout time.Duration
}

// DefaultConfig returns sensible defaults for production use.
func DefaultConfig() Config {
	return Config{
		BaseURL:   BaseURL,
		UserAgent: DefaultUserAgent,
		Rate:      300 * time.Millisecond,
		Retries:   3,
		Timeout:   30 * time.Second,
	}
}

// Client talks to the ACL Anthology and Semantic Scholar APIs.
type Client struct {
	httpClient *http.Client
	userAgent  string
	baseURL    string
	rate       time.Duration
	retries    int
	mu         sync.Mutex
	last       time.Time
}

// NewClient returns a Client configured according to cfg.
func NewClient(cfg Config) *Client {
	if cfg.BaseURL == "" {
		cfg.BaseURL = BaseURL
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = DefaultUserAgent
	}
	return &Client{
		httpClient: &http.Client{Timeout: cfg.Timeout},
		userAgent:  cfg.UserAgent,
		baseURL:    strings.TrimRight(cfg.BaseURL, "/"),
		rate:       cfg.Rate,
		retries:    cfg.Retries,
	}
}

// get fetches a URL with pacing and retries.
func (c *Client) get(ctx context.Context, rawURL string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= c.retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff(attempt)):
			}
		}
		body, retry, err := c.do(ctx, rawURL)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retry {
			return nil, err
		}
	}
	return nil, fmt.Errorf("get %s: %w", rawURL, lastErr)
}

func (c *Client) do(ctx context.Context, rawURL string) ([]byte, bool, error) {
	c.pace()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "text/html,application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, true, fmt.Errorf("http %d", resp.StatusCode)
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, false, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("http %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, true, err
	}
	return b, false, nil
}

func (c *Client) pace() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rate <= 0 {
		return
	}
	if wait := c.rate - time.Since(c.last); wait > 0 {
		time.Sleep(wait)
	}
	c.last = time.Now()
}

func backoff(attempt int) time.Duration {
	d := time.Duration(attempt) * 500 * time.Millisecond
	if d > 5*time.Second {
		d = 5 * time.Second
	}
	return d
}

// ─── Paper ───────────────────────────────────────────────────────────────────

// Paper is the record emitted for an ACL Anthology paper.
type Paper struct {
	Rank     int    `json:"rank"`
	ID       string `json:"id"`
	Title    string `json:"title"`
	Authors  string `json:"authors"`
	Year     string `json:"year"`
	Venue    string `json:"venue"`
	Abstract string `json:"abstract"`
	URL      string `json:"url"`
	PDFURL   string `json:"pdf_url"`
}

// GetPaper fetches metadata for a single paper by its ACL Anthology ID (e.g. "2024.acl-long.1").
// It parses the embedded BibTeX block on the paper page for structured fields.
func (c *Client) GetPaper(ctx context.Context, id string) (Paper, error) {
	rawURL := c.baseURL + "/" + strings.Trim(id, "/") + "/"
	body, err := c.get(ctx, rawURL)
	if err != nil {
		return Paper{}, fmt.Errorf("paper %q: %w", id, err)
	}
	p := parsePaperPage(string(body), id)
	if p.Title == "" {
		return Paper{}, fmt.Errorf("paper %q: %w", id, ErrNotFound)
	}
	p.Rank = 1
	return p, nil
}

// parsePaperPage extracts a Paper from an individual paper HTML page.
// It reads the embedded BibTeX block (inside &#34; HTML entities) which
// reliably carries title, author, year, booktitle, url, and abstract.
func parsePaperPage(html, id string) Paper {
	p := Paper{
		ID:  id,
		URL: BaseURL + "/" + strings.Trim(id, "/") + "/",
	}

	// PDF URL from the h2 title link
	if i := strings.Index(html, "<h2 id=title>"); i >= 0 {
		sub := html[i:]
		if j := strings.Index(sub, "href="); j >= 0 {
			sub = sub[j+5:]
			if k := strings.Index(sub, ">"); k >= 0 {
				href := sub[:k]
				href = strings.Trim(href, "\"' ")
				if strings.HasSuffix(href, ".pdf") {
					if strings.HasPrefix(href, "http") {
						p.PDFURL = href
					} else {
						p.PDFURL = BaseURL + href
					}
				}
			}
		}
	}

	// Parse BibTeX block embedded in the page HTML.
	// Fields are HTML-entity encoded: &#34; = "
	bib := extractBibTeX(html)
	if bib == "" {
		return p
	}
	p.Title = bibField(bib, "title")
	p.Authors = normalizeBibAuthors(bibField(bib, "author"))
	p.Year = bibField(bib, "year")
	p.Venue = bibField(bib, "booktitle")
	p.Abstract = bibField(bib, "abstract")
	if u := bibField(bib, "url"); u != "" {
		p.URL = u
	}
	return p
}

// extractBibTeX finds the @inproceedings or @article block in the HTML and
// unescapes &#34; HTML entities to recover the raw BibTeX text.
func extractBibTeX(html string) string {
	for _, marker := range []string{"@inproceedings{", "@article{", "@proceedings{"} {
		idx := strings.Index(html, marker)
		if idx < 0 {
			continue
		}
		// Find end: a line that starts with '}'
		rest := html[idx:]
		end := strings.Index(rest, "\n}")
		if end < 0 {
			continue
		}
		bib := rest[:end+2]
		bib = strings.ReplaceAll(bib, "&#34;", "\"")
		bib = strings.ReplaceAll(bib, "&#39;", "'")
		bib = strings.ReplaceAll(bib, "&amp;", "&")
		bib = strings.ReplaceAll(bib, "&lt;", "<")
		bib = strings.ReplaceAll(bib, "&gt;", ">")
		return bib
	}
	return ""
}

// bibField extracts the value of a BibTeX field by name.
// Handles both single-line and multi-line values delimited by { } or " ".
func bibField(bib, field string) string {
	needle := "\n    " + field + " = "
	idx := strings.Index(strings.ToLower(bib), strings.ToLower(needle))
	if idx < 0 {
		needle = "\n    " + field + "="
		idx = strings.Index(strings.ToLower(bib), strings.ToLower(needle))
	}
	if idx < 0 {
		return ""
	}
	rest := bib[idx+len(needle):]
	rest = strings.TrimSpace(rest)

	// value is wrapped in "..." or {...}
	if len(rest) == 0 {
		return ""
	}
	var open, close byte
	switch rest[0] {
	case '"':
		open, close = '"', '"'
	case '{':
		open, close = '{', '}'
	default:
		// bare value (e.g. month = aug)
		end := strings.IndexAny(rest, ",\n")
		if end < 0 {
			return strings.TrimSpace(rest)
		}
		return strings.TrimSpace(rest[:end])
	}
	_ = open
	// Count nested braces/quotes
	depth := 0
	var buf strings.Builder
	for i := 0; i < len(rest); i++ {
		ch := rest[i]
		if ch == '{' || (ch == '"' && depth == 0 && i == 0) {
			depth++
			if depth == 1 {
				continue
			}
		}
		if ch == close {
			depth--
			if depth == 0 {
				break
			}
		}
		buf.WriteByte(ch)
	}
	return strings.TrimSpace(buf.String())
}

// normalizeBibAuthors converts BibTeX "Last, First and Last2, First2" to
// "First Last, First2 Last2".
func normalizeBibAuthors(s string) string {
	if s == "" {
		return ""
	}
	parts := strings.Split(s, " and\n")
	if len(parts) == 1 {
		parts = strings.Split(s, " and ")
	}
	names := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// "Last, First" → "First Last"
		if comma := strings.Index(p, ","); comma >= 0 {
			last := strings.TrimSpace(p[:comma])
			first := strings.TrimSpace(p[comma+1:])
			if first != "" {
				p = first + " " + last
			} else {
				p = last
			}
		}
		names = append(names, strings.TrimSpace(p))
	}
	return strings.Join(names, ", ")
}

// ─── Volume ──────────────────────────────────────────────────────────────────

// Volume is a summary record for an ACL Anthology proceedings volume.
type Volume struct {
	Rank  int    `json:"rank"`
	ID    string `json:"id"`
	Title string `json:"title"`
	Year  string `json:"year"`
	Venue string `json:"venue"`
	Count int    `json:"count"`
	URL   string `json:"url"`
}

// ListVolume fetches the papers in a volume by volume ID (e.g. "2024.acl-long")
// and returns up to limit Paper records (0 = all).
func (c *Client) ListVolume(ctx context.Context, volumeID string, limit int) ([]Paper, error) {
	rawURL := c.baseURL + "/volumes/" + strings.Trim(volumeID, "/") + "/"
	body, err := c.get(ctx, rawURL)
	if err != nil {
		return nil, fmt.Errorf("volume %q: %w", volumeID, err)
	}
	papers := parseVolumePage(string(body), volumeID, limit)
	if len(papers) == 0 {
		return nil, fmt.Errorf("volume %q: %w", volumeID, ErrNotFound)
	}
	return papers, nil
}

// parseVolumePage extracts papers from a volume listing HTML page.
// Each paper entry follows the pattern:
//
//	<strong><a class=align-middle href=/PAPER-ID/>TITLE</a></strong>
//	<br><a href=/people/.../>Author</a>\n|\n<a href=/people/.../>Author</a>
//
// The abstract is in a card-body div that immediately follows in the HTML.
func parseVolumePage(html, volumeID string, limit int) []Paper {
	var papers []Paper
	rank := 1

	// Each paper block starts with <strong><a class=align-middle href=
	needle := "<strong><a class=align-middle href=/"
	pos := 0
	for {
		if limit > 0 && len(papers) >= limit {
			break
		}
		idx := strings.Index(html[pos:], needle)
		if idx < 0 {
			break
		}
		idx += pos
		after := html[idx+len(needle):]

		// Extract paper ID: everything up to the first '/'
		slashIdx := strings.Index(after, "/>")
		if slashIdx < 0 {
			pos = idx + 1
			continue
		}
		paperID := after[:slashIdx]

		// Extract title: between > and </a></strong>
		titleStart := strings.Index(after[slashIdx:], ">")
		if titleStart < 0 {
			pos = idx + 1
			continue
		}
		titleStart += slashIdx + 1
		titleEnd := strings.Index(after[titleStart:], "</a></strong>")
		if titleEnd < 0 {
			pos = idx + 1
			continue
		}
		title := stripHTMLTags(after[titleStart : titleStart+titleEnd])

		// Extract authors: <br><a href=/people/.../>Name</a> lines after the title
		entryEnd := strings.Index(after[titleStart+titleEnd:], "</span></div>")
		var authorStr string
		if entryEnd >= 0 {
			authorBlock := after[titleStart+titleEnd : titleStart+titleEnd+entryEnd]
			authorStr = extractAuthorsFromBlock(authorBlock)
		}

		// Extract abstract from the card-body div that follows.
		// Pattern: class="card-body p-3 small">ABSTRACT</div>
		abstractAnchor := "id=abstract-" + strings.ReplaceAll(strings.ReplaceAll(paperID, ".", "--"), "/", "--")
		abstract := ""
		if abIdx := strings.Index(html[idx:], abstractAnchor); abIdx >= 0 {
			abSection := html[idx+abIdx:]
			if cbIdx := strings.Index(abSection, "card-body p-3 small\">"); cbIdx >= 0 {
				abContent := abSection[cbIdx+len("card-body p-3 small\">"):]
				abEnd := strings.Index(abContent, "</div>")
				if abEnd >= 0 {
					abstract = strings.TrimSpace(stripHTMLTags(abContent[:abEnd]))
				}
			}
		}

		// Derive year and venue from volume ID (e.g. "2024.acl-long" → year=2024, venue=ACL)
		year, venue := extractYearVenue(volumeID)

		p := Paper{
			Rank:     rank,
			ID:       paperID,
			Title:    title,
			Authors:  authorStr,
			Year:     year,
			Venue:    venue,
			Abstract: abstract,
			URL:      BaseURL + "/" + paperID + "/",
			PDFURL:   BaseURL + "/" + paperID + ".pdf",
		}
		papers = append(papers, p)
		rank++
		pos = idx + len(needle) + titleStart + titleEnd
	}
	return papers
}

// extractAuthorsFromBlock pulls author names from a block of HTML containing
// <a href=/people/.../>Name</a> elements separated by |\n.
func extractAuthorsFromBlock(block string) string {
	var names []string
	pos := 0
	for {
		idx := strings.Index(block[pos:], "/people/")
		if idx < 0 {
			break
		}
		idx += pos
		// find end of href
		nameStart := strings.Index(block[idx:], "/>")
		if nameStart < 0 {
			break
		}
		nameStart += idx + 2
		nameEnd := strings.Index(block[nameStart:], "</a>")
		if nameEnd < 0 {
			break
		}
		name := strings.TrimSpace(block[nameStart : nameStart+nameEnd])
		if name != "" {
			names = append(names, name)
		}
		pos = nameStart + nameEnd + 4
	}
	return strings.Join(names, ", ")
}

// extractYearVenue derives year and a short venue name from a volume ID.
// "2024.acl-long" → ("2024", "ACL")
// "2023.emnlp-main" → ("2023", "EMNLP")
func extractYearVenue(volumeID string) (string, string) {
	parts := strings.SplitN(volumeID, ".", 2)
	if len(parts) < 2 {
		return "", volumeID
	}
	year := parts[0]
	rest := parts[1]
	// rest is like "acl-long", "emnlp-main", "naacl-srw"
	dash := strings.Index(rest, "-")
	if dash >= 0 {
		rest = rest[:dash]
	}
	return year, strings.ToUpper(rest)
}

// ─── Events ──────────────────────────────────────────────────────────────────

// Event is a record for an ACL Anthology event (a conference year).
type Event struct {
	Rank    int    `json:"rank"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	Year    string `json:"year"`
	Volumes int    `json:"volumes"`
	URL     string `json:"url"`
}

// ListEvents fetches the events index and returns up to limit events (0 = all),
// newest first.
func (c *Client) ListEvents(ctx context.Context, limit int) ([]Event, error) {
	rawURL := c.baseURL + "/events/"
	body, err := c.get(ctx, rawURL)
	if err != nil {
		return nil, fmt.Errorf("events: %w", err)
	}
	events := parseEventsPage(string(body), limit)
	return events, nil
}

// parseEventsPage extracts events from the events index HTML.
// Event links appear as <a href=/events/EVENT-ID/> in anchor elements.
func parseEventsPage(html string, limit int) []Event {
	// Events are grouped by year. Parse anchor tags with /events/ID/ pattern.
	// The page has event names in list items.
	seen := map[string]bool{}
	var events []Event
	rank := 1

	needle := "href=/events/"
	pos := 0
	for {
		if limit > 0 && len(events) >= limit {
			break
		}
		idx := strings.Index(html[pos:], needle)
		if idx < 0 {
			break
		}
		idx += pos

		after := html[idx+len(needle):]
		slashIdx := strings.Index(after, "/")
		if slashIdx < 0 {
			pos = idx + 1
			continue
		}
		eventID := after[:slashIdx]
		if eventID == "" || seen[eventID] {
			pos = idx + 1
			continue
		}
		seen[eventID] = true

		// event name is the anchor text
		nameStart := strings.Index(after[slashIdx:], ">")
		name := ""
		if nameStart >= 0 {
			nameStart += slashIdx + 1
			nameEnd := strings.Index(after[nameStart:], "</a>")
			if nameEnd >= 0 {
				name = strings.TrimSpace(after[nameStart : nameStart+nameEnd])
			}
		}
		if name == "" {
			name = eventID
		}

		// event IDs are like "acl-2024", "eacl-2026", "naacl-2024", "crowdmt-2023"
		// year is always the last dash-separated component that looks like a year
		year := ""
		parts := strings.Split(eventID, "-")
		for i := len(parts) - 1; i >= 0; i-- {
			if len(parts[i]) == 4 && parts[i] >= "1950" && parts[i] <= "2100" {
				year = parts[i]
				break
			}
		}

		e := Event{
			Rank: rank,
			ID:   eventID,
			Name: name,
			Year: year,
			URL:  BaseURL + "/events/" + eventID + "/",
		}
		events = append(events, e)
		rank++
		pos = idx + 1
	}
	return events
}

// ─── Recent ──────────────────────────────────────────────────────────────────

// recentVenues lists main venue volume IDs for the current and previous year,
// newest first. These are fetched for the `recent` command.
var recentVenues = []string{
	"2024.acl-long",
	"2024.emnlp-main",
	"2024.naacl-long",
	"2024.eacl-long",
	"2024.findings-acl",
	"2023.acl-long",
	"2023.emnlp-main",
}

// Recent returns up to limit papers from the most recent major ACL-org venues.
// It fetches volumes in order until limit is reached.
func (c *Client) Recent(ctx context.Context, limit int) ([]Paper, error) {
	if limit <= 0 {
		limit = 20
	}
	var out []Paper
	for _, vol := range recentVenues {
		if len(out) >= limit {
			break
		}
		need := limit - len(out)
		papers, err := c.ListVolume(ctx, vol, need)
		if err != nil {
			continue
		}
		out = append(out, papers...)
	}
	// Re-rank
	for i := range out {
		out[i].Rank = i + 1
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ─── Search ──────────────────────────────────────────────────────────────────

// defaultSearchVolumes is the ordered list of volumes searched by Search when
// Semantic Scholar is unavailable. Covers the last three years of major venues.
var defaultSearchVolumes = []string{
	"2024.acl-long",
	"2024.acl-short",
	"2024.findings-acl",
	"2024.emnlp-main",
	"2024.emnlp-short",
	"2024.findings-emnlp",
	"2024.naacl-long",
	"2024.naacl-short",
	"2024.eacl-long",
	"2024.eacl-short",
	"2023.acl-long",
	"2023.acl-short",
	"2023.findings-acl",
	"2023.emnlp-main",
	"2023.emnlp-short",
	"2023.findings-emnlp",
	"2023.naacl-long",
	"2023.naacl-short",
	"2022.acl-long",
	"2022.acl-short",
	"2022.emnlp-main",
	"2022.naacl-main",
}

// Search queries the Semantic Scholar public API for papers published in ACL
// Anthology venues. When S2 is unavailable (429 or network error), it falls
// back to keyword-matching over recent ACL Anthology volume pages.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]Paper, error) {
	if limit <= 0 {
		limit = 10
	}

	// Try Semantic Scholar first (no API key needed, ~1 req/s limit).
	out, s2err := c.searchS2(ctx, query, limit)
	if s2err == nil && len(out) > 0 {
		return out, nil
	}

	// Fall back to keyword search over recent ACL Anthology volumes.
	return c.searchVolumes(ctx, query, limit)
}

// searchS2 queries Semantic Scholar and filters to ACL Anthology papers.
func (c *Client) searchS2(ctx context.Context, query string, limit int) ([]Paper, error) {
	fields := "title,authors,year,venue,abstract,externalIds,openAccessPdf"
	pageSize := limit * 3
	if pageSize > 100 {
		pageSize = 100
	}

	var out []Paper
	offset := 0
	for len(out) < limit {
		params := url.Values{}
		params.Set("query", query)
		params.Set("fields", fields)
		params.Set("limit", fmt.Sprintf("%d", pageSize))
		params.Set("offset", fmt.Sprintf("%d", offset))
		rawURL := s2BaseURL + "/paper/search?" + params.Encode()

		body, err := c.get(ctx, rawURL)
		if err != nil {
			return out, fmt.Errorf("search: %w", err)
		}
		var resp s2SearchResp
		if err := json.Unmarshal(body, &resp); err != nil {
			return out, fmt.Errorf("search decode: %w", err)
		}
		if len(resp.Data) == 0 {
			break
		}
		for _, hit := range resp.Data {
			aclID := hit.ExternalIDs["ACL"]
			if aclID == "" {
				continue
			}
			p := s2HitToPaper(hit, aclID, len(out)+1)
			out = append(out, p)
			if len(out) >= limit {
				break
			}
		}
		offset += len(resp.Data)
		if resp.Next == 0 || offset >= resp.Total {
			break
		}
		if offset > limit*20 {
			break
		}
	}
	return out, nil
}

// searchVolumes does a keyword search over recent ACL Anthology volume pages.
// It fetches each volume in turn and filters papers whose title or abstract
// contains any of the query terms (case-insensitive).
func (c *Client) searchVolumes(ctx context.Context, query string, limit int) ([]Paper, error) {
	terms := tokenize(query)
	var out []Paper
	for _, volID := range defaultSearchVolumes {
		if len(out) >= limit {
			break
		}
		// Fetch the full volume (no limit so we can filter).
		papers, err := c.ListVolume(ctx, volID, 0)
		if err != nil {
			continue
		}
		for _, p := range papers {
			if len(out) >= limit {
				break
			}
			if matchTerms(p, terms) {
				p.Rank = len(out) + 1
				out = append(out, p)
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no papers matching %q in recent ACL Anthology volumes", query)
	}
	return out, nil
}

// tokenize splits a query into lowercase terms.
func tokenize(query string) []string {
	parts := strings.Fields(strings.ToLower(query))
	var terms []string
	for _, p := range parts {
		p = strings.Trim(p, `"'.,;:!?`)
		if p != "" {
			terms = append(terms, p)
		}
	}
	return terms
}

// matchTerms reports whether every term appears in the paper's title, authors,
// abstract, or venue (case-insensitive).
func matchTerms(p Paper, terms []string) bool {
	haystack := strings.ToLower(p.Title + " " + p.Authors + " " + p.Abstract + " " + p.Venue)
	for _, t := range terms {
		if !strings.Contains(haystack, t) {
			return false
		}
	}
	return true
}

// s2SearchResp is the Semantic Scholar search response envelope.
type s2SearchResp struct {
	Total  int       `json:"total"`
	Offset int       `json:"offset"`
	Next   int       `json:"next"`
	Data   []s2Paper `json:"data"`
}

// s2Paper is a wire paper from Semantic Scholar.
type s2Paper struct {
	PaperID     string            `json:"paperId"`
	Title       string            `json:"title"`
	Year        int               `json:"year"`
	Venue       string            `json:"venue"`
	Abstract    string            `json:"abstract"`
	ExternalIDs map[string]string `json:"externalIds"`
	Authors     []s2Author        `json:"authors"`
	OpenAccPDF  *s2OpenAccPDF     `json:"openAccessPdf"`
}

type s2Author struct {
	AuthorID string `json:"authorId"`
	Name     string `json:"name"`
}

type s2OpenAccPDF struct {
	URL string `json:"url"`
}

func s2HitToPaper(h s2Paper, aclID string, rank int) Paper {
	names := make([]string, len(h.Authors))
	for i, a := range h.Authors {
		names[i] = a.Name
	}
	year := ""
	if h.Year > 0 {
		year = fmt.Sprintf("%d", h.Year)
	}
	pdfURL := ""
	if h.OpenAccPDF != nil && h.OpenAccPDF.URL != "" {
		pdfURL = h.OpenAccPDF.URL
	}
	if pdfURL == "" {
		pdfURL = BaseURL + "/" + aclID + ".pdf"
	}
	return Paper{
		Rank:     rank,
		ID:       aclID,
		Title:    h.Title,
		Authors:  strings.Join(names, ", "),
		Year:     year,
		Venue:    h.Venue,
		Abstract: h.Abstract,
		URL:      BaseURL + "/" + aclID + "/",
		PDFURL:   pdfURL,
	}
}

// ─── HTML helpers ─────────────────────────────────────────────────────────────

// stripHTMLTags removes HTML tags and decodes common HTML entities.
func stripHTMLTags(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			b.WriteRune(r)
		}
	}
	out := b.String()
	out = strings.ReplaceAll(out, "&amp;", "&")
	out = strings.ReplaceAll(out, "&lt;", "<")
	out = strings.ReplaceAll(out, "&gt;", ">")
	out = strings.ReplaceAll(out, "&quot;", `"`)
	out = strings.ReplaceAll(out, "&#39;", "'")
	out = strings.ReplaceAll(out, "&apos;", "'")
	out = strings.ReplaceAll(out, "&nbsp;", " ")
	out = strings.ReplaceAll(out, "&#34;", `"`)
	return strings.TrimSpace(out)
}
