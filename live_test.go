package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/mxschmitt/playwright-go"
)

func TestLiveDownloads(t *testing.T) {
	address := os.Getenv("FFASTLOADER_LIVE_URL")
	if address == "" {
		t.Skip("set FFASTLOADER_LIVE_URL to run the live browser/download check")
	}
	if err := validateURL(address); err != nil {
		t.Fatal(err)
	}
	pw, err := playwright.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer pw.Stop()
	configDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(configDir, "ffastloader", "browser")
	config := Config{Headless: true, Timeout: 30, DownloadDir: t.TempDir()}
	session, err := newBrowserSession(pw, profile, config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if session != nil {
			session.context.Close()
		}
	}()
	links, err := extractUrls(address, session)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, group := range groupDownloadLinks(links) {
		if len(group.Files) >= 5 {
			files = group.Files[:5]
			break
		}
	}
	if len(files) != 5 {
		t.Fatal("live check needs a listing with at least five archive parts")
	}
	if err := session.context.Close(); err != nil {
		t.Fatal(err)
	}
	downloader, err := newFileDownloader(config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		downloader.resolver.CloseIdleConnections()
		downloader.transfer.CloseIdleConnections()
	}()
	logger := NewConsoleLogger(6, 5, "Checking live downloads")
	for _, address := range files[:2] {
		if err := downloader.download(context.Background(), address, logger, 1); err != nil {
			t.Fatal(err)
		}
		logger.UpdateProgress(1)
	}
	downloader.resolver.CloseIdleConnections()
	downloader.transfer.CloseIdleConnections()
	downloader, err = newFileDownloader(config)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errors := make(chan error, 3)
	for i, address := range files[2:] {
		wg.Add(1)
		go func(worker int, link string) {
			defer wg.Done()
			errors <- downloader.download(context.Background(), link, logger, worker)
			logger.UpdateProgress(1)
		}(i+1, address)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(config.DownloadDir)
	if err != nil || len(entries) != 5 {
		t.Fatalf("expected five completed archives: entries=%v error=%v", entries, err)
	}
	for _, entry := range entries {
		file, err := os.Open(filepath.Join(config.DownloadDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		header := make([]byte, 6)
		_, readErr := io.ReadFull(file, header)
		info, statErr := file.Stat()
		file.Close()
		if readErr != nil || statErr != nil || string(header) != "Rar!\x1a\x07" {
			t.Fatalf("%s is not a completed RAR download: header=%q read=%v stat=%v", entry.Name(), header, readErr, statErr)
		}
		t.Logf("Verified %s: %d bytes", entry.Name(), info.Size())
	}
}
