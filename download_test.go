package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tlsclient "github.com/bogdanfinn/tls-client"
)

type localResolver struct {
	tlsclient.HttpClient
	target *url.URL
}

func (c localResolver) Do(request *fhttp.Request) (*fhttp.Response, error) {
	request.URL.Scheme, request.URL.Host = c.target.Scheme, c.target.Host
	return c.HttpClient.Do(request)
}

type localTransfer struct {
	http.RoundTripper
	target *url.URL
}

func (c localTransfer) RoundTrip(request *http.Request) (*http.Response, error) {
	request.URL.Scheme, request.URL.Host = c.target.Scheme, c.target.Host
	return c.RoundTripper.RoundTrip(request)
}

func TestFileDownloader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/f/"), "/go")
			if r.Header.Get("HX-Request") != "true" || r.Header.Get("Referer") != "https://fuckingfast.co/"+id {
				t.Errorf("invalid file resolution request: %s %s", r.Method, r.URL.Path)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			switch id {
			case "blocked":
				w.Header().Set("cf-mitigated", "challenge")
				w.WriteHeader(http.StatusForbidden)
			case "missing":
			case "external":
				w.Header().Set("HX-Redirect", "https://example.com/dl/untrusted")
			default:
				w.Header().Set("HX-Redirect", "https://dl.fuckingfast.co/dl/"+id)
			}
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/dl/")
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.rar"`, id))
		switch id {
		case "unsafe":
			w.Header().Set("Content-Disposition", `attachment; filename="../escape.rar"`)
		case "short":
			w.Header().Set("Content-Length", "100")
		case "html":
			w.Header().Set("Content-Type", "text/html")
		case "range":
			w.Header().Set("Content-Range", "bytes 10-19/20")
			w.WriteHeader(http.StatusPartialContent)
		case "fullrange":
			body := "payload for " + id
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(body)-1, len(body)))
			w.WriteHeader(http.StatusPartialContent)
		case "cancel":
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		case "long":
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			select {
			case <-time.After(1200 * time.Millisecond):
			case <-r.Context().Done():
				return
			}
		case "headers":
			select {
			case <-time.After(2 * time.Second):
			case <-r.Context().Done():
				return
			}
		}
		fmt.Fprint(w, "payload for "+id)
	}))
	defer server.Close()
	config := Config{Timeout: 1, DownloadDir: t.TempDir()}
	downloader, err := newFileDownloader(config)
	if err != nil {
		t.Fatal(err)
	}
	defer downloader.resolver.CloseIdleConnections()
	defer downloader.transfer.CloseIdleConnections()
	local, _ := url.Parse(server.URL)
	downloader.resolver = localResolver{downloader.resolver, local}
	downloader.transfer.Transport = localTransfer{downloader.transfer.Transport, local}
	logger := NewConsoleLogger(1, 5, "Processing files")
	var wg sync.WaitGroup
	for i, id := range []string{"one", "two", "three", "long", "fullrange"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := downloader.download(context.Background(), "https://fuckingfast.co/"+id+"#fallback.rar", logger, i+1); err != nil {
				t.Errorf("download %s: %v", id, err)
				return
			}
			body, err := os.ReadFile(filepath.Join(config.DownloadDir, id+".rar"))
			if err != nil || string(body) != "payload for "+id {
				t.Errorf("saved %s: %q error=%v", id, body, err)
			}
		}()
	}
	wg.Wait()
	for _, test := range []struct {
		id, message string
		blocked     bool
	}{
		{"blocked", "HTTP 403", true},
		{"missing", "valid download URL", false},
		{"external", "valid download URL", false},
		{"unsafe", "unsafe filename", false},
		{"short", "transfer download", false},
		{"html", "HTML instead", false},
		{"range", "incomplete byte range", false},
		{"headers", "timeout", false},
	} {
		t.Run(test.id, func(t *testing.T) {
			name := filepath.Join(config.DownloadDir, test.id+".rar")
			if err := os.WriteFile(name, []byte("existing download"), 0600); err != nil {
				t.Fatal(err)
			}
			err := downloader.download(context.Background(), "https://fuckingfast.co/"+test.id, logger, 1)
			if err == nil || !strings.Contains(err.Error(), test.message) || errors.Is(err, errAccessBlocked) != test.blocked {
				t.Fatalf("download %s: %v", test.id, err)
			}
			if test.id == "headers" {
				var timeout net.Error
				if !errors.As(err, &timeout) || !timeout.Timeout() {
					t.Fatalf("download startup did not time out: %v", err)
				}
			}
			body, err := os.ReadFile(name)
			if err != nil || string(body) != "existing download" {
				t.Fatalf("failed download replaced the existing file: %q error=%v", body, err)
			}
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := downloader.download(ctx, "https://fuckingfast.co/cancel", logger, 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("download did not cancel: %v", err)
	}
	partials, err := filepath.Glob(filepath.Join(config.DownloadDir, ".ffastloader-*.part"))
	if err != nil || len(partials) != 0 {
		t.Fatalf("incomplete downloads left temporary files: %v error=%v", partials, err)
	}
}
