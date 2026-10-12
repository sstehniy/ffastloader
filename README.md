# Fucking Loader

A command-line tool for downloading multi-part archives from paste.fitgirl-repacks.site and fuckingfast.co/d/ links.

## Features

- Download multi-part archives with automatic grouping
- Interactive selection menu for choosing which file groups to download
- Concurrent downloads with configurable worker count
- Automatic retry for failed downloads
- Clean, focused UI with fixed progress bar
- Cross-platform: works on Windows, macOS, and Linux

## Installation

### Download Pre-built Binaries

Pre-built binaries are available for download from the [Releases](https://github.com/sstehniy/ffastloader/releases) page.

Choose the appropriate version for your platform:
- Windows: `ffastloader-windows-amd64.exe`
- macOS (Intel): `ffastloader-darwin-amd64`
- macOS (Apple Silicon): `ffastloader-darwin-arm64`
- Linux (x64): `ffastloader-linux-amd64`
- Linux (ARM64): `ffastloader-linux-arm64`

### Build from Source

If you prefer to build from source, you'll need Go 1.24.1 or later:

```bash
# Clone the repository
git clone https://github.com/sstehniy/ffastloader.git
cd ffastloader

# Install dependencies
go mod download

# Build the program
go build -o ffastloader
```

## Usage

```bash
# Basic usage
./ffastloader "https://paste.fitgirl-repacks.site/your-paste-url"

# With options
./ffastloader --workers 5 --dir "downloads" --timeout 60 --retry 5 "https://paste.fitgirl-repacks.site/your-paste-url"
```

File downloads use the host's direct download endpoint with a Chrome-compatible TLS handshake. The app does not open individual file pages or click download buttons. Files transfer directly to your computer; no proxy or CAPTCHA service is used. Incomplete transfers use temporary files and never replace an existing completed file.

A hidden browser extracts links from the listing, including encrypted paste pages, then closes before file selection. If the listing itself requires verification, rerun with `--headless=false` to show that browser. Its profile is stored under your system's user configuration directory (`~/Library/Application Support/ffastloader/browser` on macOS).

If the file endpoint refuses a request, the app reports the HTTP error and stops the queue. Failed files and their errors remain in the final summary. The progress bar counts completed files, so it stays at 0% until the first file finishes. Ctrl+C cancels active transfers and removes their temporary files.

### Command-line Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--workers` | 3 | Number of concurrent download workers |
| `--dir` | "downloads" | Directory to save downloads |
| `--timeout` | 30 | Timeout in seconds for navigation, page controls, and download startup; file transfers can take longer |
| `--retry` | 3 | Number of retry attempts for failed downloads |
| `--headless` | true | Hide the browser used to extract listing links |
| `--skip-selection` | false | Skip file group selection and download all files |
| `--log-lines` | 3 | Number of log lines to display during download |

### Verification

Tests cover listing extraction, verification pages, cookie reuse, concurrent direct downloads, truncated responses, cancellation, and preservation of existing files:

```bash
go run github.com/mxschmitt/playwright-go/cmd/playwright@v0.6201.1 install chromium
go test -race ./...
```

The terminal selection test also needs Python 3 and `tput` on macOS/Linux.

## Interactive Selection

The program provides an interactive selection menu that allows you to choose which file groups to download:

- Navigate with ↑/↓ arrow keys
- Toggle selection with SPACE
- Confirm selection with ENTER
- Quit with ESC or Q

## Continuous Integration

This repository is configured with GitHub Actions to automatically build and release new versions when code is pushed to the master branch or a PR is merged.

The automation:
1. Builds binaries for Windows, macOS (Intel & Apple Silicon), and Linux
2. Creates a new release with versioned binaries
3. Generates release notes based on commits since the last release

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request. 
