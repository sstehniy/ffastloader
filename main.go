package main

import (
	"bufio"
	"container/ring"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/eiannone/keyboard"
	"github.com/playwright-community/playwright-go"
	"github.com/schollz/progressbar/v3"
)

// Config holds all program configuration
type Config struct {
	StartURL      string
	WorkerCount   int
	DownloadDir   string
	Timeout       int
	RetryAttempts int
	Headless      bool
	SkipSelection bool
	LogLines      int
}

// FileGroup represents a group of related files (multiple parts of the same archive)
type FileGroup struct {
	Name     string
	Files    []string
	Selected bool
}

// ConsoleLogger provides a way to log messages while maintaining a fixed progress bar
type ConsoleLogger struct {
	maxLines    int
	messages    *ring.Ring
	progressBar *progressbar.ProgressBar
	mutex       sync.Mutex
}

// NewConsoleLogger creates a new logger with fixed progress bar
func NewConsoleLogger(maxLines int, totalItems int64, description string) *ConsoleLogger {
	return &ConsoleLogger{
		maxLines: maxLines,
		messages: ring.New(maxLines),
		progressBar: progressbar.NewOptions64(
			totalItems,
			progressbar.OptionSetDescription(description),
			progressbar.OptionSetItsString("file"),
			progressbar.OptionShowIts(),
			progressbar.OptionSetTheme(progressbar.Theme{
				Saucer:        "=",
				SaucerHead:    ">",
				SaucerPadding: " ",
				BarStart:      "[",
				BarEnd:        "]",
			}),
		),
	}
}

// Log adds a message to the ring buffer and redraws the console
func (cl *ConsoleLogger) Log(format string, args ...interface{}) {
	cl.mutex.Lock()
	defer cl.mutex.Unlock()

	// Format the message with timestamp
	message := fmt.Sprintf("[%s] %s",
		time.Now().Format("15:04:05"),
		fmt.Sprintf(format, args...))

	// Add the message to the ring buffer
	cl.messages.Value = message
	cl.messages = cl.messages.Next()

	// Redraw the console
	cl.redraw()
}

// UpdateProgress updates the progress bar value
func (cl *ConsoleLogger) UpdateProgress(n int) {
	cl.mutex.Lock()
	defer cl.mutex.Unlock()

	cl.progressBar.Add(n)
	cl.redraw()
}

// redraw clears the console and redraws the progress bar and messages
func (cl *ConsoleLogger) redraw() {
	// Clear screen from cursor to end of screen
	fmt.Print("\033[J")

	// Move cursor to beginning of line and print progress bar
	fmt.Print("\033[H")
	fmt.Println(cl.progressBar.String())
	fmt.Println()

	// Print the last n messages
	messages := make([]string, 0, cl.maxLines)
	cl.messages.Do(func(v interface{}) {
		if v != nil {
			messages = append(messages, v.(string))
		}
	})

	// Sort messages to maintain chronological order
	for _, msg := range messages {
		if msg != "" {
			fmt.Println(msg)
		}
	}
}

// Finalize prints a final message and resets terminal
func (cl *ConsoleLogger) Finalize(message string) {
	cl.mutex.Lock()
	defer cl.mutex.Unlock()

	// Move cursor to beginning of line
	fmt.Print("\033[H")

	// Clear screen from cursor to end of screen
	fmt.Print("\033[J")

	// Print the progress bar one last time
	fmt.Println(cl.progressBar.String())
	fmt.Println()

	// Print final message
	fmt.Println(message)
}

func validateURL(rawURL string) error {
	parsedURL, err := url.Parse(rawURL)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return fmt.Errorf("invalid URL: must be a valid absolute URL")
	}

	host := strings.ToLower(parsedURL.Hostname())
	if host == "paste.fitgirl-repacks.site" || host == "www.paste.fitgirl-repacks.site" || host == "fuckingfast.co" || host == "www.fuckingfast.co" {
		return nil
	}

	return fmt.Errorf("invalid URL: host must be paste.fitgirl-repacks.site or fuckingfast.co")
}

// groupDownloadLinks organizes download links into related groups
func groupDownloadLinks(links []string) []FileGroup {
	// Create a map to store groups
	groups := make(map[string]*FileGroup)

	// Regular expression to extract the base name from filenames
	// Matches patterns like "filename.part001.rar", "filename.part_001.rar", etc.
	re := regexp.MustCompile(`^(.+?)\.part[_.]?(\d+)\.rar$`)

	for _, link := range links {
		// Extract the actual filename part from the URL
		// For URLs like https://fuckingfast.co/hash#God_of_War_Ragnarok_--_fitgirl-repacks.site_--_.part001.rar
		// we need to get the part after the # symbol
		var filename string

		if hashIndex := strings.LastIndex(link, "#"); hashIndex != -1 {
			// Extract the part after the hash
			filename = link[hashIndex+1:]
		} else {
			// If there's no hash, use the base name of the URL path
			filename = filepath.Base(link)
		}

		// Try to match the filename to our pattern
		// Convert to lowercase for case-insensitive matching
		matches := re.FindStringSubmatch(strings.ToLower(filename))
		if len(matches) >= 2 {
			// Extract the base name (without the part number)
			baseName := matches[1]

			// Create a new group if this base name hasn't been seen before
			if _, exists := groups[baseName]; !exists {
				groups[baseName] = &FileGroup{
					Name:     baseName,
					Files:    []string{},
					Selected: true, // Default to selected
				}
			}

			// Add this file to its group
			groups[baseName].Files = append(groups[baseName].Files, link)
		} else {
			// For files that don't match the pattern, create a single-file group
			baseName := strings.TrimSuffix(filename, filepath.Ext(filename))
			if _, exists := groups[baseName]; !exists {
				groups[baseName] = &FileGroup{
					Name:     baseName,
					Files:    []string{link},
					Selected: true,
				}
			} else {
				groups[baseName].Files = append(groups[baseName].Files, link)
			}
		}
	}

	// Sort the files in each group by part number if possible
	sortFileGroupsByPartNumber(groups)

	// Convert the map to a slice for easier handling
	var result []FileGroup
	for _, group := range groups {
		result = append(result, *group)
	}

	return result
}

// sortFileGroupsByPartNumber sorts files in each group by their part number
func sortFileGroupsByPartNumber(groups map[string]*FileGroup) {
	partRegex := regexp.MustCompile(`\.part[_.]?(\d+)\.rar$`)

	for _, group := range groups {
		// Sort the files in each group by part number
		sort.SliceStable(group.Files, func(i, j int) bool {
			// Extract part numbers from both filenames
			filenameI := extractFilenameFromURL(group.Files[i])
			filenameJ := extractFilenameFromURL(group.Files[j])

			matchesI := partRegex.FindStringSubmatch(strings.ToLower(filenameI))
			matchesJ := partRegex.FindStringSubmatch(strings.ToLower(filenameJ))

			// If either doesn't match the pattern, maintain original order
			if len(matchesI) < 2 || len(matchesJ) < 2 {
				return i < j
			}

			// Parse part numbers and compare
			partI, _ := strconv.Atoi(matchesI[1])
			partJ, _ := strconv.Atoi(matchesJ[1])
			return partI < partJ
		})
	}
}

// extractFilenameFromURL gets the actual filename from a URL
func extractFilenameFromURL(url string) string {
	if hashIndex := strings.LastIndex(url, "#"); hashIndex != -1 {
		return url[hashIndex+1:]
	}
	return filepath.Base(url)
}

// interactiveSelection displays an interactive menu to select file groups
func interactiveSelection(groups []FileGroup) []FileGroup {
	// Make a copy of the groups to avoid modifying the original
	selectedGroups := make([]FileGroup, len(groups))
	copy(selectedGroups, groups)

	// Initialize keyboard
	if err := keyboard.Open(); err != nil {
		log.Printf("Failed to open keyboard: %v", err)
		log.Println("Falling back to non-interactive mode")
		return promptForSelection(groups)
	}
	defer keyboard.Close()

	currentPos := 0

	// Function to clear screen and print the current selection state
	redrawMenu := func() {
		// Clear screen (ANSI escape code to clear screen and move cursor to 0,0)
		fmt.Print("\033[H\033[2J")

		fmt.Println("Navigate with ↑/↓ arrows, toggle selection with SPACE, confirm with ENTER, quit with ESC or Q")
		fmt.Println("\nSelect which file groups to download:")

		for i, group := range selectedGroups {
			// Show an indicator for the current cursor position
			cursor := " "
			if i == currentPos {
				cursor = ">"
			}

			// Show the selection status
			status := " "
			if group.Selected {
				status = "X"
			}

			fileCount := len(group.Files)

			// Get a sample filename to show
			var sampleName string
			if fileCount > 0 {
				sampleName = extractFilenameFromURL(group.Files[0])
				// If this is a multi-part archive, indicate the range
				if fileCount > 1 {
					lastSample := extractFilenameFromURL(group.Files[fileCount-1])
					sampleName = fmt.Sprintf("%s ... %s", sampleName, lastSample)
				}
			}

			fmt.Printf("%s %d. [%s] %s (%d %s)\n", cursor, i+1, status, group.Name, fileCount, pluralize("file", fileCount))
			if i == currentPos {
				fmt.Printf("      Sample: %s\n", sampleName)
			}
		}

		// Count selected groups and files
		selectedCount := 0
		totalFiles := 0
		for _, group := range selectedGroups {
			if group.Selected {
				selectedCount++
				totalFiles += len(group.Files)
			}
		}

		fmt.Printf("\nCurrently selected: %d of %d groups (%d total files)\n",
			selectedCount, len(selectedGroups), totalFiles)
	}

	// Initial draw
	redrawMenu()

	// Event loop for keyboard input
	for {
		char, key, err := keyboard.GetKey()
		if err != nil {
			log.Printf("Error reading keyboard: %v", err)
			break
		}

		switch key {
		case keyboard.KeyArrowUp:
			// Move cursor up
			if currentPos > 0 {
				currentPos--
			}
		case keyboard.KeyArrowDown:
			// Move cursor down
			if currentPos < len(selectedGroups)-1 {
				currentPos++
			}
		case keyboard.KeySpace:
			// Toggle selection
			selectedGroups[currentPos].Selected = !selectedGroups[currentPos].Selected
		case keyboard.KeyEnter:
			// Confirm selection
			// Count selected groups
			selectedCount := 0
			for _, group := range selectedGroups {
				if group.Selected {
					selectedCount++
				}
			}

			if selectedCount == 0 {
				fmt.Println("\nWarning: No groups selected. Please select at least one group.")
				time.Sleep(2 * time.Second)
				redrawMenu()
				continue
			}

			// Final confirmation
			fmt.Print("\nConfirm selection? (Y/n): ")
			char, _, _ = keyboard.GetKey()
			if char == 'n' || char == 'N' {
				redrawMenu()
				continue
			}

			return selectedGroups
		case keyboard.KeyEsc:
			// Exit
			fmt.Println("\nOperation cancelled by user.")
			os.Exit(0)
		default:
			// Handle regular keys
			if char == 'q' || char == 'Q' {
				fmt.Println("\nOperation cancelled by user.")
				os.Exit(0)
			}
		}

		// Redraw menu after each key press
		redrawMenu()
	}

	// If we exit the loop due to an error, return the current selection
	return selectedGroups
}

// promptForSelection displays file groups and allows user to select which to download
// This is kept as a fallback in case keyboard control is not available
func promptForSelection(groups []FileGroup) []FileGroup {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("\nThe following file groups were found. Enter the numbers of groups you want to EXCLUDE, separated by space:")
	for i, group := range groups {
		fileCount := len(group.Files)

		// Get a sample filename to show
		var sampleName string
		if fileCount > 0 {
			sampleName = extractFilenameFromURL(group.Files[0])
			// If this is a multi-part archive, indicate the range
			if fileCount > 1 {
				lastSample := extractFilenameFromURL(group.Files[fileCount-1])
				sampleName = fmt.Sprintf("%s ... %s", sampleName, lastSample)
			}
		}

		fmt.Printf("%d. [X] %s (%d %s)\n", i+1, group.Name, fileCount, pluralize("file", fileCount))
		fmt.Printf("   Sample: %s\n", sampleName)
	}

	fmt.Print("\nEnter numbers to exclude (or press Enter to download all): ")
	scanner.Scan()
	input := scanner.Text()

	if input == "" {
		return groups // No changes, download all
	}

	// Parse the numbers entered by the user
	excludeNumbers := strings.Fields(input)
	for _, numStr := range excludeNumbers {
		num, err := strconv.Atoi(numStr)
		if err != nil || num < 1 || num > len(groups) {
			fmt.Printf("Warning: Invalid input '%s' ignored\n", numStr)
			continue
		}

		// Unselect the group (zero-indexed in the array, but 1-indexed in the display)
		groups[num-1].Selected = false
	}

	// Display the final selection
	fmt.Println("\nSelected groups for download:")
	selectedCount := 0
	totalFiles := 0
	for i, group := range groups {
		mark := " "
		if group.Selected {
			mark = "X"
			selectedCount++
			totalFiles += len(group.Files)
		}
		fmt.Printf("%d. [%s] %s (%d files)\n", i+1, mark, group.Name, len(group.Files))
	}

	if selectedCount == 0 {
		fmt.Println("Warning: No groups selected. Exiting.")
		os.Exit(0)
	}

	fmt.Printf("\nWill download %d of %d groups (%d total files).\n", selectedCount, len(groups), totalFiles)
	fmt.Print("Press Enter to continue or Ctrl+C to abort... ")
	scanner.Scan() // Wait for user confirmation

	return groups
}

func pluralize(word string, count int) string {
	if count == 1 {
		return word
	}
	return word + "s"
}

func main() {
	// Parse command line flags
	config := Config{}

	flag.IntVar(&config.WorkerCount, "workers", 3, "Number of concurrent download workers")
	flag.StringVar(&config.DownloadDir, "dir", "downloads", "Directory to save downloads")
	flag.IntVar(&config.Timeout, "timeout", 30, "Timeout in seconds for network operations")
	flag.IntVar(&config.RetryAttempts, "retry", 3, "Number of retry attempts for failed downloads")
	flag.BoolVar(&config.Headless, "headless", true, "Run browser in headless mode")
	flag.BoolVar(&config.SkipSelection, "skip-selection", false, "Skip file group selection and download all files")
	flag.IntVar(&config.LogLines, "log-lines", 3, "Number of log lines to display during download")

	flag.Parse()

	// Check if a URL was provided
	args := flag.Args()
	if len(args) < 1 {
		log.Fatal("Usage: program [flags] <starturl>\nRun with -h for help")
	}

	config.StartURL = args[0]

	// Create downloads directory if it doesn't exist
	if err := os.MkdirAll(config.DownloadDir, 0755); err != nil {
		log.Fatalf("Failed to create downloads directory: %v", err)
	}

	// Validate the URL
	if err := validateURL(config.StartURL); err != nil {
		log.Fatal(err)
	}

	log.Printf("Starting download from: %s", config.StartURL)
	log.Printf("Download directory: %s", config.DownloadDir)
	log.Printf("Using %d workers", config.WorkerCount)

	// Install Playwright if needed
	if err := playwright.Install(); err != nil {
		log.Fatalf("Failed to install Playwright driver: %v", err)
	}

	// Start Playwright and launch the browser
	pw, err := playwright.Run()
	if err != nil {
		log.Fatalf("Could not start Playwright: %v", err)
	}

	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(config.Headless),
	})
	if err != nil {
		log.Fatalf("Could not launch browser: %v", err)
	}

	defer func() {
		if err := browser.Close(); err != nil {
			log.Printf("Could not close browser: %v", err)
		}
		if err := pw.Stop(); err != nil {
			log.Printf("Could not stop Playwright: %v", err)
		}
	}()

	// Extract download links
	log.Println("Extracting download links...")
	links, err := extractUrls(config.StartURL, browser)
	if err != nil {
		log.Fatalf("Failed to extract URLs: %v", err)
	}
	log.Printf("Found %d links to download", len(links))

	// Group the links by their base names
	groups := groupDownloadLinks(links)
	log.Printf("Organized into %d distinct file groups", len(groups))

	// Allow user to select which groups to download (unless skipped)
	if !config.SkipSelection {
		groups = interactiveSelection(groups)
	}

	// Flatten the selected groups back into a list of links to download
	var selectedLinks []string
	for _, group := range groups {
		if group.Selected {
			selectedLinks = append(selectedLinks, group.Files...)
		}
	}

	if len(selectedLinks) == 0 {
		log.Println("No files selected for download. Exiting.")
		return
	}

	log.Printf("Preparing to download %d files", len(selectedLinks))

	// Clear the screen before starting the download process
	fmt.Print("\033[H\033[2J")

	// Create the console logger with fixed progress bar
	logger := NewConsoleLogger(config.LogLines, int64(len(selectedLinks)), "Downloading files")

	// Create a channel for the URLs
	jobs := make(chan string, len(selectedLinks))
	results := make(chan bool, len(selectedLinks))
	var wg sync.WaitGroup

	// Launch worker pool
	for i := 0; i < config.WorkerCount; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for url := range jobs {
				success := false
				for attempt := 1; attempt <= config.RetryAttempts; attempt++ {
					if attempt > 1 {
						logger.Log("[Worker %d] Retry attempt %d/%d for %s",
							workerID, attempt, config.RetryAttempts, url)
					}

					if downloadRarWithLogger(url, browser, config, logger, workerID) {
						success = true
						break
					}

					// Wait before retrying
					if attempt < config.RetryAttempts {
						time.Sleep(time.Duration(attempt) * 2 * time.Second)
					}
				}

				results <- success
				logger.UpdateProgress(1)
			}
		}(i + 1)
	}

	// Feed job channel with URLs
	for _, link := range selectedLinks {
		jobs <- link
	}
	close(jobs)

	// Collect results
	successCount := 0
	go func() {
		for success := range results {
			if success {
				successCount++
			}
		}
	}()

	// Wait for workers to finish
	wg.Wait()
	close(results)

	// Show the final results
	logger.Finalize(fmt.Sprintf("Downloads completed: %d/%d successful\nAll operations completed.",
		successCount, len(selectedLinks)))
}

const BUTTON_SELECTOR = ".link-button.text-5xl"

// downloadRar navigates to a URL and downloads the matching RAR file.
// Returns true if download was successful, false otherwise.
func downloadRar(link string, browser playwright.Browser, config Config) bool {
	// Create a new page for the download.
	page, err := browser.NewPage(playwright.BrowserNewPageOptions{
		AcceptDownloads: playwright.Bool(true),
	})
	if err != nil {
		log.Printf("[%s] Failed to create page: %v", link, err)
		return false
	}
	defer page.Close()

	// Set a custom timeout based on config
	timeout := float64(config.Timeout * 1000) // Convert seconds to ms

	// Navigate to the download page.
	_, err = page.Goto(link, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateNetworkidle,
		Timeout:   playwright.Float(timeout),
	})
	if err != nil {
		log.Printf("[%s] Navigation failed: %v", link, err)
		return false
	}

	// Wait for the button to be visible.
	button := page.Locator(BUTTON_SELECTOR)
	if err = button.WaitFor(playwright.LocatorWaitForOptions{
		State:   playwright.WaitForSelectorStateVisible,
		Timeout: playwright.Float(timeout / 3), // Shorter timeout for UI elements
	}); err != nil {
		log.Printf("[%s] Button not found: %v", link, err)
		return false
	}

	// Perform the first click.
	log.Printf("[%s] Performing first click...", link)
	if err = button.Click(playwright.LocatorClickOptions{
		Timeout: playwright.Float(timeout / 3),
	}); err != nil {
		log.Printf("[%s] First click failed: %v", link, err)
		return false
	}

	// Allow some time for the page to update.
	time.Sleep(2 * time.Second)
	if err = button.WaitFor(playwright.LocatorWaitForOptions{
		State:   playwright.WaitForSelectorStateVisible,
		Timeout: playwright.Float(timeout / 3),
	}); err != nil {
		log.Printf("[%s] Button not visible after first click: %v", link, err)
		return false
	}

	// Use ExpectDownload to wait for the download event after the second click.
	log.Printf("[%s] Performing second click to start download...", link)
	download, err := page.ExpectDownload(func() error {
		return button.Click(playwright.LocatorClickOptions{
			Timeout: playwright.Float(timeout / 3),
		})
	})
	if err != nil {
		log.Printf("[%s] Download event error: %v", link, err)
		return false
	}

	// Retrieve the suggested filename.
	suggestedName := download.SuggestedFilename()

	downloadPath := filepath.Join(config.DownloadDir, suggestedName)
	log.Printf("[%s] Starting download of: %s", link, suggestedName)

	// Save the downloaded file.
	if err = download.SaveAs(downloadPath); err != nil {
		log.Printf("[%s] Failed to save download: %v", link, err)
		return false
	}

	log.Printf("[%s] Download completed: %s", link, downloadPath)
	return true
}

// extractUrls extracts URLs from the given page.
// Supports both old paste list markup and new table-based markup.
func extractUrls(startURL string, browser playwright.Browser) ([]string, error) {
	var links []string
	seen := make(map[string]struct{})

	// Reject direct file URLs and require a listing page.
	if parsedURL, err := url.Parse(startURL); err == nil {
		host := strings.ToLower(parsedURL.Hostname())
		if (host == "fuckingfast.co" || host == "www.fuckingfast.co") && !strings.HasPrefix(parsedURL.Path, "/d/") {
			return nil, fmt.Errorf("invalid URL: use a fuckingfast listing URL in /d/<id> format or in the paste.fitgirl-repacks.site/<id> format")
		}
	}

	page, err := browser.NewPage()
	if err != nil {
		return nil, fmt.Errorf("could not create page: %w", err)
	}
	defer page.Close()

	_, err = page.Goto(startURL, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateNetworkidle,
	})
	if err != nil {
		return nil, fmt.Errorf("navigation failed: %w", err)
	}

	selectors := []string{"#tbody tr td a", "#plaintext ul li a"}
	baseURL, _ := url.Parse(startURL)

	for _, selector := range selectors {
		entries, err := page.Locator(selector).All()
		if err != nil {
			log.Printf("warning: could not get entries for selector %q: %v", selector, err)
			continue
		}

		for _, entry := range entries {
			href, err := entry.GetAttribute("href")
			if err != nil {
				log.Printf("warning: could not get href attribute: %v", err)
				continue
			}

			href = strings.TrimSpace(href)
			if href == "" {
				continue
			}

			resolvedHref := href
			if baseURL != nil {
				if hrefURL, hrefErr := url.Parse(href); hrefErr == nil {
					resolvedHref = baseURL.ResolveReference(hrefURL).String()
				}
			}

			if _, exists := seen[resolvedHref]; exists {
				continue
			}
			seen[resolvedHref] = struct{}{}
			links = append(links, resolvedHref)
		}
	}

	if len(links) == 0 {
		return nil, fmt.Errorf("no links found on the page (checked selectors: #tbody tr td a, #plaintext ul li a)")
	}

	return links, nil
}

// downloadRarWithLogger navigates to a URL and downloads the matching RAR file.
// Uses the ConsoleLogger for output. Returns true if download was successful, false otherwise.
func downloadRarWithLogger(link string, browser playwright.Browser, config Config, logger *ConsoleLogger, workerID int) bool {
	// Create a new page for the download.
	page, err := browser.NewPage(playwright.BrowserNewPageOptions{
		AcceptDownloads: playwright.Bool(true),
	})
	if err != nil {
		logger.Log("[Worker %d] Failed to create page: %v", workerID, err)
		return false
	}
	defer page.Close()

	// Set a custom timeout based on config
	timeout := float64(config.Timeout * 1000) // Convert seconds to ms

	// Extract the filename from the link for logging
	filename := extractFilenameFromURL(link)

	// Navigate to the download page.
	logger.Log("[Worker %d] Navigating to download page for %s", workerID, filename)
	_, err = page.Goto(link, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateNetworkidle,
		Timeout:   playwright.Float(timeout),
	})
	if err != nil {
		logger.Log("[Worker %d] Navigation failed: %v", workerID, err)
		return false
	}

	// Wait for the button to be visible.
	button := page.Locator(BUTTON_SELECTOR)
	if err = button.WaitFor(playwright.LocatorWaitForOptions{
		State:   playwright.WaitForSelectorStateVisible,
		Timeout: playwright.Float(timeout / 3), // Shorter timeout for UI elements
	}); err != nil {
		logger.Log("[Worker %d] Button not found: %v", workerID, err)
		return false
	}

	// Perform the first click.
	logger.Log("[Worker %d] Performing first click for %s", workerID, filename)
	if err = button.Click(playwright.LocatorClickOptions{
		Timeout: playwright.Float(timeout / 3),
	}); err != nil {
		logger.Log("[Worker %d] First click failed: %v", workerID, err)
		return false
	}

	// Allow some time for the page to update.
	time.Sleep(2 * time.Second)
	if err = button.WaitFor(playwright.LocatorWaitForOptions{
		State:   playwright.WaitForSelectorStateVisible,
		Timeout: playwright.Float(timeout / 3),
	}); err != nil {
		logger.Log("[Worker %d] Button not visible after first click: %v", workerID, err)
		return false
	}

	// Use ExpectDownload to wait for the download event after the second click.
	logger.Log("[Worker %d] Performing second click to start download...", workerID)
	download, err := page.ExpectDownload(func() error {
		return button.Click(playwright.LocatorClickOptions{
			Timeout: playwright.Float(timeout / 3),
		})
	})
	if err != nil {
		logger.Log("[Worker %d] Download event error: %v", workerID, err)
		return false
	}

	// Retrieve the suggested filename.
	suggestedName := download.SuggestedFilename()

	downloadPath := filepath.Join(config.DownloadDir, suggestedName)
	logger.Log("[Worker %d] Starting download of: %s", workerID, suggestedName)

	// Save the downloaded file.
	if err = download.SaveAs(downloadPath); err != nil {
		logger.Log("[Worker %d] Failed to save download: %v", workerID, err)
		return false
	}

	logger.Log("[Worker %d] Download completed: %s", workerID, suggestedName)
	return true
}
