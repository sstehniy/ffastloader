package main

import (
	"bufio"
	"container/ring"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/eiannone/keyboard"
	"github.com/mxschmitt/playwright-go"
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
			progressbar.OptionSetWriter(io.Discard),
			progressbar.OptionSetRenderBlankState(true),
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
	fmt.Print("\033[H\033[J")
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

	if runtime.GOOS != "windows" {
		keypadStart, startErr := exec.Command("tput", "smkx").Output()
		keypadEnd, endErr := exec.Command("tput", "rmkx").Output()
		if startErr != nil || endErr != nil {
			keyboard.Close()
			log.Println("Could not configure terminal keys; falling back to number selection")
			return promptForSelection(groups)
		}
		// Terminfo key sequences expect keypad transmit mode, which keyboard.Open does not enable.
		fmt.Print(string(keypadStart))
		defer fmt.Print(string(keypadEnd))
	}

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
			return nil
		default:
			// Handle regular keys
			if char == 'q' || char == 'Q' {
				fmt.Println("\nOperation cancelled by user.")
				return nil
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
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	// Parse command line flags
	config := Config{}

	flag.IntVar(&config.WorkerCount, "workers", 3, "Number of concurrent download workers")
	flag.StringVar(&config.DownloadDir, "dir", "downloads", "Directory to save downloads")
	flag.IntVar(&config.Timeout, "timeout", 30, "Timeout in seconds for network operations")
	flag.IntVar(&config.RetryAttempts, "retry", 3, "Number of retry attempts for failed downloads")
	flag.BoolVar(&config.Headless, "headless", true, "Hide the browser used for link extraction")
	flag.BoolVar(&config.SkipSelection, "skip-selection", false, "Skip file group selection and download all files")
	flag.IntVar(&config.LogLines, "log-lines", 3, "Number of log lines to display during download")

	flag.Parse()

	// Check if a URL was provided
	args := flag.Args()
	if len(args) < 1 {
		return fmt.Errorf("usage: ffastloader [flags] <starturl>\nRun with -h for help")
	}

	config.StartURL = args[0]

	if config.WorkerCount < 1 || config.RetryAttempts < 1 || config.Timeout < 1 || config.LogLines < 1 {
		return fmt.Errorf("workers, retry, timeout, and log-lines must all be at least 1")
	}

	// Validate the URL
	if err := validateURL(config.StartURL); err != nil {
		return err
	}
	if err := os.MkdirAll(config.DownloadDir, 0755); err != nil {
		return fmt.Errorf("create download directory: %w", err)
	}

	log.Printf("Starting download from: %s", config.StartURL)
	log.Printf("Download directory: %s", config.DownloadDir)
	log.Printf("Using %d workers", config.WorkerCount)

	// Install Playwright if needed
	if err := playwright.Install(&playwright.RunOptions{Browsers: []string{"chromium"}}); err != nil {
		return fmt.Errorf("install Playwright: %w", err)
	}

	// Start Playwright and launch the browser
	pw, err := playwright.Run()
	if err != nil {
		return fmt.Errorf("start Playwright: %w", err)
	}
	defer pw.Stop()
	configDir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("find browser profile directory: %w", err)
	}
	session, err := newBrowserSession(pw, filepath.Join(configDir, "ffastloader", "browser"), config)
	if err != nil {
		return err
	}
	defer func() {
		if err := session.context.Close(); err != nil {
			log.Printf("Could not close browser: %v", err)
		}
	}()

	// Extract download links
	log.Println("Extracting download links...")
	links, err := extractUrls(config.StartURL, session)
	if err != nil {
		return fmt.Errorf("extract URLs: %w", err)
	}
	if err := session.context.Close(); err != nil {
		return fmt.Errorf("close link extraction browser: %w", err)
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
		return nil
	}

	downloader, err := newFileDownloader(config)
	if err != nil {
		return err
	}
	defer downloader.resolver.CloseIdleConnections()
	defer downloader.transfer.CloseIdleConnections()
	log.Printf("Preparing to download %d files", len(selectedLinks))

	// Clear the screen before starting the download process
	fmt.Print("\033[H\033[2J")

	// Create the console logger with fixed progress bar
	logger := NewConsoleLogger(config.LogLines, int64(len(selectedLinks)), "Processing files")

	// Create a channel for the URLs
	jobs := make(chan string, len(selectedLinks))
	type downloadResult struct {
		link string
		err  error
	}
	results := make(chan downloadResult, len(selectedLinks))
	var wg sync.WaitGroup
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Launch worker pool
	for i := 0; i < config.WorkerCount; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for url := range jobs {
				if ctx.Err() != nil {
					return
				}
				var downloadErr error
			retries:
				for attempt := 1; attempt <= config.RetryAttempts; attempt++ {
					if attempt > 1 {
						logger.Log("[Worker %d] Retry attempt %d/%d for %s",
							workerID, attempt, config.RetryAttempts, url)
					}

					downloadErr = downloader.download(ctx, url, logger, workerID)
					if downloadErr == nil {
						break
					}
					if ctx.Err() != nil {
						break
					}
					logger.Log("[Worker %d] %s: %v", workerID, extractFilenameFromURL(url), downloadErr)
					if errors.Is(downloadErr, errAccessBlocked) {
						cancel()
						break
					}

					// Wait before retrying
					if attempt < config.RetryAttempts {
						select {
						case <-ctx.Done():
							break retries
						case <-time.After(time.Duration(attempt) * 2 * time.Second):
						}
					}
				}

				results <- downloadResult{link: url, err: downloadErr}
				logger.UpdateProgress(1)
			}
		}(i + 1)
	}

	// Feed job channel with URLs
	for _, link := range selectedLinks {
		jobs <- link
	}
	close(jobs)

	// Wait for workers to finish
	wg.Wait()
	close(results)
	successCount := 0
	var failures []string
	for result := range results {
		if result.err == nil {
			successCount++
		} else {
			failures = append(failures, fmt.Sprintf("%s: %v", extractFilenameFromURL(result.link), result.err))
		}
	}

	// Show the final results
	summary := fmt.Sprintf("Downloads completed: %d/%d successful", successCount, len(selectedLinks))
	if len(failures) > 0 {
		summary += "\n" + strings.Join(failures, "\n")
	}
	logger.Finalize(summary)
	if successCount != len(selectedLinks) {
		return fmt.Errorf("%d of %d downloads did not complete", len(selectedLinks)-successCount, len(selectedLinks))
	}
	return nil
}

// extractUrls extracts URLs from the given page.
// Supports both old paste list markup and new table-based markup.
func extractUrls(startURL string, session *browserSession) ([]string, error) {
	var links []string
	seen := make(map[string]struct{})

	// Reject direct file URLs and require a listing page.
	if parsedURL, err := url.Parse(startURL); err == nil {
		host := strings.ToLower(parsedURL.Hostname())
		if (host == "fuckingfast.co" || host == "www.fuckingfast.co") && !strings.HasPrefix(parsedURL.Path, "/d/") {
			return nil, fmt.Errorf("invalid URL: use a fuckingfast listing URL in /d/<id> format or in the paste.fitgirl-repacks.site/<id> format")
		}
	}

	page, err := session.openPage(startURL, "#tbody tr td a, #plaintext ul li a", log.Printf)
	if err != nil {
		return nil, err
	}
	defer page.Close()

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
