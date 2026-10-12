package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mxschmitt/playwright-go"
)

func TestBrowserSession(t *testing.T) {
	const readySelector = "#ready"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/table":
			http.SetCookie(w, &http.Cookie{Name: "verified", Value: "yes", Path: "/", MaxAge: 3600})
			fmt.Fprint(w, `<table><tbody id="tbody"><tr><td><a href="/file/one#one.rar">one</a><a href="/file/two#two.rar">two</a><a href="/file/one#one.rar">duplicate</a></td></tr></tbody></table>`)
		case r.URL.Path == "/plaintext":
			fmt.Fprint(w, `<div id="plaintext"></div><script>setTimeout(() => document.querySelector('#plaintext').innerHTML = '<ul><li><a href="/file/three#three.rar">three</a></li></ul>', 200)</script>`)
		case r.URL.Path == "/blocked":
			w.Header().Set("cf-mitigated", "challenge")
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `<title>Just a moment...</title>Performing security verification`)
		case r.URL.Path == "/forbidden":
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `<div id="ready">misleading control</div>`)
		case r.URL.Path == "/automatic":
			w.Header().Set("cf-mitigated", "challenge")
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `<title>Just a moment...</title><script>setTimeout(() => location.href = '/file/one', 200)</script>`)
		case strings.HasPrefix(r.URL.Path, "/file/"):
			if _, err := r.Cookie("verified"); err != nil {
				w.Header().Set("cf-mitigated", "challenge")
				w.WriteHeader(http.StatusForbidden)
				fmt.Fprint(w, `<title>Just a moment...</title>`)
				return
			}
			fmt.Fprint(w, `<div id="ready">ready</div>`)
		default:
			fmt.Fprint(w, "advertisement")
		}
	}))
	defer server.Close()

	pw, err := playwright.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer pw.Stop()
	profile := filepath.Join(t.TempDir(), "browser")
	config := Config{Headless: true, Timeout: 2, DownloadDir: t.TempDir()}
	session, err := newBrowserSession(pw, profile, config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if session != nil {
			session.context.Close()
		}
	}()

	links, err := extractUrls(server.URL+"/table", session)
	if err != nil || len(links) != 2 || links[0] != server.URL+"/file/one#one.rar" || links[1] != server.URL+"/file/two#two.rar" {
		t.Fatalf("table extraction: links=%v error=%v", links, err)
	}
	plainLinks, err := extractUrls(server.URL+"/plaintext", session)
	if err != nil || len(plainLinks) != 1 || plainLinks[0] != server.URL+"/file/three#three.rar" {
		t.Fatalf("plaintext extraction: links=%v error=%v", plainLinks, err)
	}
	if err := session.context.Close(); err != nil {
		t.Fatal(err)
	}
	session, err = newBrowserSession(pw, profile, config)
	if err != nil {
		t.Fatal(err)
	}
	page, err := session.openPage(server.URL+"/file/one", readySelector, log.Printf)
	if err != nil {
		t.Fatalf("verification cookie did not survive browser restart: %v", err)
	}
	page.Close()

	for _, path := range []string{"/blocked", "/forbidden"} {
		page, err := session.openPage(server.URL+path, readySelector, log.Printf)
		if page != nil || !errors.Is(err, errAccessBlocked) {
			t.Fatalf("%s should report blocked access: page=%v error=%v", path, page, err)
		}
	}
	page, err = session.openPage(server.URL+"/automatic", readySelector, log.Printf)
	if err != nil {
		t.Fatalf("automatically completed verification: %v", err)
	}
	page.Close()

}

func TestConsoleLoggerRedraw(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = writer, writer
	defer func() { os.Stdout, os.Stderr = stdout, stderr }()
	logger := NewConsoleLogger(1, 2, "Processing files")
	logger.Log("a long previous message")
	logger.Log("short")
	logger.UpdateProgress(1)
	logger.Finalize("download failed: Cloudflare verification required")
	writer.Close()
	output, err := io.ReadAll(reader)
	reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	frames := strings.Split(string(output), "\033[H\033[J")
	if len(frames) != 5 || frames[0] != "" {
		t.Fatalf("redraw must move home before clearing: %q", output)
	}
	for _, frame := range frames[1:] {
		if strings.Count(frame, "Processing files") != 1 {
			t.Fatalf("progress bar was printed more than once: %q", frame)
		}
	}
	if strings.Contains(frames[2], "previous") || !strings.Contains(frames[4], "Cloudflare verification required") {
		t.Fatalf("old message or final error rendered incorrectly: %q", output)
	}
}
