package main

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tlsclient "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

type fileDownloader struct {
	resolver tlsclient.HttpClient
	transfer *http.Client
	config   Config
}

func newFileDownloader(config Config) (*fileDownloader, error) {
	resolver, err := tlsclient.NewHttpClient(tlsclient.NewNoopLogger(),
		tlsclient.WithClientProfile(profiles.Chrome_150),
		tlsclient.WithTimeoutSeconds(config.Timeout),
		tlsclient.WithNotFollowRedirects(),
	)
	if err != nil {
		return nil, fmt.Errorf("create download client: %w", err)
	}
	timeout := time.Duration(config.Timeout) * time.Second
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}).DialContext
	transport.TLSHandshakeTimeout = timeout
	transport.ResponseHeaderTimeout = timeout
	return &fileDownloader{
		resolver: resolver,
		transfer: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
		config: config,
	}, nil
}

var fileIDPattern = regexp.MustCompile(`^[A-Za-z0-9]+$`)

func (d *fileDownloader) download(ctx context.Context, link string, logger *ConsoleLogger, workerID int) error {
	fileURL, err := url.Parse(link)
	if err != nil {
		return fmt.Errorf("parse file URL: %w", err)
	}
	host := strings.ToLower(fileURL.Host)
	fileID := strings.Trim(fileURL.Path, "/")
	if fileURL.Scheme != "https" || fileURL.User != nil || (host != "fuckingfast.co" && host != "www.fuckingfast.co") || !fileIDPattern.MatchString(fileID) {
		return fmt.Errorf("invalid fuckingfast file URL")
	}
	pageURL := "https://fuckingfast.co/" + fileID
	filename := extractFilenameFromURL(link)
	logger.Log("[Worker %d] Resolving download for %s", workerID, filename)
	request, err := fhttp.NewRequestWithContext(ctx, "POST", "https://fuckingfast.co/f/"+fileID+"/go", nil)
	if err != nil {
		return fmt.Errorf("create file request: %w", err)
	}
	for key, value := range map[string]string{
		"Accept": "*/*", "Accept-Language": "en-US,en;q=0.9",
		"User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36",
		"HX-Request": "true", "HX-Current-URL": pageURL,
		"Origin": "https://fuckingfast.co", "Referer": pageURL,
		"Sec-Fetch-Dest": "empty", "Sec-Fetch-Mode": "cors", "Sec-Fetch-Site": "same-origin",
	} {
		request.Header.Set(key, value)
	}
	response, err := d.resolver.Do(request)
	if err != nil {
		return fmt.Errorf("resolve download: %w", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("cf-mitigated") == "challenge" {
		return downloadHTTPError(response.StatusCode, response.Header.Get("cf-mitigated"))
	}
	target, err := url.Parse(strings.TrimSpace(response.Header.Get("HX-Redirect")))
	if err != nil || target.Scheme != "https" || target.User != nil || (target.Host != "dl.fuckingfast.co" && target.Host != "fuckingfast.co") || !strings.HasPrefix(target.Path, "/dl/") {
		return fmt.Errorf("file server did not provide a valid download URL")
	}
	fileRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return fmt.Errorf("create download request: %w", err)
	}
	file, err := d.transfer.Do(fileRequest)
	if err != nil {
		return fmt.Errorf("start download: %w", err)
	}
	defer file.Body.Close()
	if (file.StatusCode != http.StatusOK && file.StatusCode != http.StatusPartialContent) || file.Header.Get("cf-mitigated") == "challenge" {
		return downloadHTTPError(file.StatusCode, file.Header.Get("cf-mitigated"))
	}
	if strings.Contains(strings.ToLower(file.Header.Get("Content-Type")), "html") {
		return fmt.Errorf("file server returned HTML instead of a file")
	}
	expectedSize := file.ContentLength
	if file.StatusCode == http.StatusPartialContent {
		var start, end, total int64
		if _, err := fmt.Sscanf(file.Header.Get("Content-Range"), "bytes %d-%d/%d", &start, &end, &total); err != nil || start != 0 || end < start || end+1 != total {
			return fmt.Errorf("file server returned an incomplete byte range")
		}
		expectedSize = total
	}
	if _, params, err := mime.ParseMediaType(file.Header.Get("Content-Disposition")); err == nil && params["filename"] != "" {
		filename = params["filename"]
	}
	if filename == "" || filename == "." || filename == ".." || strings.ContainsAny(filename, `/\`) {
		return fmt.Errorf("file server provided an unsafe filename")
	}
	partial, err := os.CreateTemp(d.config.DownloadDir, ".ffastloader-*.part")
	if err != nil {
		return fmt.Errorf("create download file: %w", err)
	}
	defer os.Remove(partial.Name())
	defer partial.Close()
	logger.Log("[Worker %d] Starting download of: %s", workerID, filename)
	written, err := io.Copy(partial, file.Body)
	if err != nil {
		return fmt.Errorf("transfer download: %w", err)
	}
	if expectedSize >= 0 && written != expectedSize {
		return fmt.Errorf("incomplete download: received %d of %d bytes", written, expectedSize)
	}
	if err := partial.Close(); err != nil {
		return fmt.Errorf("close download file: %w", err)
	}
	if err := os.Rename(partial.Name(), filepath.Join(d.config.DownloadDir, filename)); err != nil {
		return fmt.Errorf("save download: %w", err)
	}
	logger.Log("[Worker %d] Download completed: %s", workerID, filename)
	return nil
}

func downloadHTTPError(status int, mitigated string) error {
	if status == http.StatusForbidden || status == http.StatusUnauthorized || mitigated == "challenge" {
		return fmt.Errorf("%w: download request returned HTTP %d", errAccessBlocked, status)
	}
	return fmt.Errorf("download request returned HTTP %d", status)
}
