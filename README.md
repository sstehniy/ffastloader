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

Pre-built binaries are available for download from the [Releases](https://github.com/sstehniy/fuckingloader/releases) page.

Choose the appropriate version for your platform:
- Windows: `fuckingloader-windows-amd64.exe`
- macOS (Intel): `fuckingloader-darwin-amd64`
- macOS (Apple Silicon): `fuckingloader-darwin-arm64`
- Linux (x64): `fuckingloader-linux-amd64`
- Linux (ARM64): `fuckingloader-linux-arm64`

### Build from Source

If you prefer to build from source, you'll need Go 1.18 or later:

```bash
# Clone the repository
git clone https://github.com/sstehniy/fuckingloader.git
cd fuckingloader

# Install dependencies
go mod download

# Build the program
go build -o fuckingloader
```

## Usage

```bash
# Basic usage
./fuckingloader "https://paste.fitgirl-repacks.site/your-paste-url"

# With options
./fuckingloader --workers 5 --dir "downloads" --timeout 60 --retry 5 "https://paste.fitgirl-repacks.site/your-paste-url"
```

### Command-line Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--workers` | 3 | Number of concurrent download workers |
| `--dir` | "downloads" | Directory to save downloads |
| `--timeout` | 30 | Timeout in seconds for network operations |
| `--retry` | 3 | Number of retry attempts for failed downloads |
| `--headless` | true | Run browser in headless mode (true/false) |
| `--skip-selection` | false | Skip file group selection and download all files |
| `--log-lines` | 3 | Number of log lines to display during download |

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