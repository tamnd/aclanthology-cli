package aclanthology_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tamnd/aclanthology-cli/aclanthology"
)

// sampleVolumePage is a minimal volume listing with two paper entries.
const sampleVolumePage = `<!doctype html><html><body>
<div class="d-sm-flex align-items-stretch mb-3">
<strong><a class=align-middle href=/2024.acl-long.1/>Quantized Side Tuning</a></strong>
<br><a href=/people/alice/>Alice Smith</a>
|
<a href=/people/bob/>Bob Jones</a>
</span></div>
<div class="card-body p-3 small" id=abstract-2024--acl-long--1>This is the abstract for paper 1.</div>
<div class="d-sm-flex align-items-stretch mb-3">
<strong><a class=align-middle href=/2024.acl-long.2/>Second Paper Title</a></strong>
<br><a href=/people/carol/>Carol White</a>
</span></div>
</body></html>`

// samplePaperPage is a minimal paper page with embedded BibTeX.
const samplePaperPage = `<!doctype html><html><body>
<h2 id=title><a href=https://aclanthology.org/2024.acl-long.1.pdf>Quantized Side Tuning</a></h2>
@inproceedings{zhang-etal-2024-quantized,
    title = &#34;Quantized Side Tuning&#34;,
    author = &#34;Zhang, Zhengxin  and
      Zhao, Dan&#34;,
    booktitle = &#34;Proceedings of ACL 2024&#34;,
    year = &#34;2024&#34;,
    url = &#34;https://aclanthology.org/2024.acl-long.1/&#34;,
    abstract = &#34;Finetuning large language models is effective.&#34;
}
</body></html>`

func newTestClient(t *testing.T, handler http.HandlerFunc) (*aclanthology.Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	cfg := aclanthology.DefaultConfig()
	cfg.BaseURL = srv.URL
	cfg.Rate = 0
	cfg.Retries = 1
	client := aclanthology.NewClient(cfg)
	return client, srv
}

func TestGetPaper(t *testing.T) {
	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("request carried no User-Agent")
		}
		_, _ = w.Write([]byte(samplePaperPage))
	})
	defer srv.Close()

	p, err := client.GetPaper(context.Background(), "2024.acl-long.1")
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "Quantized Side Tuning" {
		t.Errorf("Title = %q, want %q", p.Title, "Quantized Side Tuning")
	}
	if !strings.Contains(p.Authors, "Zhengxin Zhang") {
		t.Errorf("Authors %q does not contain Zhengxin Zhang", p.Authors)
	}
	if p.Year != "2024" {
		t.Errorf("Year = %q, want %q", p.Year, "2024")
	}
	if !strings.Contains(p.Abstract, "Finetuning") {
		t.Errorf("Abstract %q does not contain expected text", p.Abstract)
	}
	if p.PDFURL == "" {
		t.Error("PDFURL is empty")
	}
}

func TestListVolume(t *testing.T) {
	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sampleVolumePage))
	})
	defer srv.Close()

	papers, err := client.ListVolume(context.Background(), "2024.acl-long", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(papers) < 2 {
		t.Fatalf("got %d papers, want at least 2", len(papers))
	}
	if papers[0].Title != "Quantized Side Tuning" {
		t.Errorf("papers[0].Title = %q", papers[0].Title)
	}
	if papers[0].Authors == "" {
		t.Error("papers[0].Authors is empty")
	}
	if papers[1].Title != "Second Paper Title" {
		t.Errorf("papers[1].Title = %q", papers[1].Title)
	}
}

func TestListVolumeLimit(t *testing.T) {
	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sampleVolumePage))
	})
	defer srv.Close()

	papers, err := client.ListVolume(context.Background(), "2024.acl-long", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(papers) != 1 {
		t.Errorf("got %d papers with limit=1, want 1", len(papers))
	}
}

func TestGetRetriesOn503(t *testing.T) {
	var hits int
	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(samplePaperPage))
	})
	defer srv.Close()
	// Need more retries for this test
	cfg := aclanthology.DefaultConfig()
	cfg.BaseURL = srv.URL
	cfg.Rate = 0
	cfg.Retries = 5
	client = aclanthology.NewClient(cfg)

	p, err := client.GetPaper(context.Background(), "2024.acl-long.1")
	if err != nil {
		t.Fatal(err)
	}
	if p.Title == "" {
		t.Error("Title is empty after retries")
	}
	if hits != 3 {
		t.Errorf("server saw %d hits, want 3", hits)
	}
}

func TestListEvents(t *testing.T) {
	const sampleEvents = `<html><body>
<ul>
<li><a href=/events/acl-2024/>ACL 2024</a></li>
<li><a href=/events/emnlp-2024/>EMNLP 2024</a></li>
<li><a href=/events/naacl-2024/>NAACL 2024</a></li>
</ul>
</body></html>`

	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sampleEvents))
	})
	defer srv.Close()

	events, err := client.ListEvents(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) < 3 {
		t.Fatalf("got %d events, want at least 3", len(events))
	}
	if events[0].ID != "acl-2024" {
		t.Errorf("events[0].ID = %q, want acl-2024", events[0].ID)
	}
	if events[0].Year != "2024" {
		t.Errorf("events[0].Year = %q, want 2024", events[0].Year)
	}
}

func TestSearchFallback(t *testing.T) {
	// The test server serves the sample volume page for any /volumes/ request.
	// Search falls back to volume search when S2 is unavailable.
	client, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/volumes/") {
			_, _ = w.Write([]byte(sampleVolumePage))
			return
		}
		// Simulate S2 rate limit to trigger fallback.
		w.WriteHeader(http.StatusTooManyRequests)
	})
	defer srv.Close()

	// "Quantized" appears in the sample volume page's first paper title.
	papers, err := client.Search(context.Background(), "Quantized", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(papers) == 0 {
		t.Error("expected at least one paper matching 'Quantized' in sample volume")
	}
}
