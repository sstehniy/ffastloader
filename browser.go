package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/mxschmitt/playwright-go"
)

var errAccessBlocked = errors.New("access blocked")

type browserSession struct {
	context      playwright.BrowserContext
	config       Config
	verification sync.Mutex
}

func newBrowserSession(pw *playwright.Playwright, profileDir string, config Config) (*browserSession, error) {
	if err := os.MkdirAll(profileDir, 0700); err != nil {
		return nil, fmt.Errorf("create browser profile: %w", err)
	}
	browserContext, err := pw.Chromium.LaunchPersistentContext(profileDir, playwright.BrowserTypeLaunchPersistentContextOptions{
		Headless:        playwright.Bool(config.Headless),
		Channel:         playwright.String("chromium"),
		AcceptDownloads: playwright.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("launch browser: %w", err)
	}
	return &browserSession{context: browserContext, config: config}, nil
}

func (s *browserSession) openPage(address, readySelector string, report func(string, ...interface{})) (playwright.Page, error) {
	page, err := s.context.NewPage()
	if err != nil {
		return nil, fmt.Errorf("create page: %w", err)
	}
	page.OnPopup(func(popup playwright.Page) {
		if err := popup.Close(); err != nil {
			report("Could not close ad popup: %v", err)
		}
	})
	if err := s.preparePage(page, address, readySelector, report); err != nil {
		page.Close()
		return nil, err
	}
	return page, nil
}

func (s *browserSession) preparePage(page playwright.Page, address, readySelector string, report func(string, ...interface{})) error {
	timeout := float64(s.config.Timeout * 1000)
	response, err := page.Goto(address, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		Timeout:   playwright.Float(timeout),
	})
	if err != nil {
		return fmt.Errorf("navigation failed: %w", err)
	}
	if response == nil {
		return fmt.Errorf("navigation returned no HTTP response for %s", address)
	}
	title, err := page.Title()
	if err != nil {
		return fmt.Errorf("read page title: %w", err)
	}
	challenge := response.Headers()["cf-mitigated"] == "challenge" || title == "Just a moment..."
	if challenge {
		s.verification.Lock()
		defer s.verification.Unlock()
		if s.config.Headless {
			report("Cloudflare verification detected; waiting up to %d seconds", s.config.Timeout)
		} else {
			timeout = 120000
			if err := page.BringToFront(); err != nil {
				return fmt.Errorf("show verification page: %w", err)
			}
			report("Complete Cloudflare verification in the browser. Waiting up to 2 minutes.")
		}
	} else if response.Status() >= 400 {
		if response.Status() == 403 || response.Status() == 401 {
			return fmt.Errorf("%w: HTTP %d from %s", errAccessBlocked, response.Status(), address)
		}
		return fmt.Errorf("HTTP %d from %s", response.Status(), address)
	}
	if err := page.Locator(readySelector).First().WaitFor(playwright.LocatorWaitForOptions{
		State:   playwright.WaitForSelectorStateVisible,
		Timeout: playwright.Float(timeout),
	}); err != nil {
		if challenge {
			return fmt.Errorf("%w: Cloudflare verification did not complete; rerun with --headless=false and complete the check in the browser", errAccessBlocked)
		}
		return fmt.Errorf("page controls not ready: %w", err)
	}
	return nil
}
