# WordPress Website Downloader

A robust Go-based tool for downloading WordPress websites for offline viewing. This project allows you to create a complete offline copy of a WordPress website while maintaining its structure, assets, and navigation.

## Features

- Downloads complete WordPress sites with all assets (images, CSS, JavaScript, fonts, etc.)
- Processes CSS files to extract and download referenced resources
- Handles WordPress-specific URL patterns and cache-busting query parameters
- Supports multilingual sites with proper language directory handling
- Removes Google Analytics, Google Tag Manager, and other tracking scripts
- Converts absolute URLs to relative for proper offline navigation
- Maintains the original directory structure
- Handles srcset attributes in image tags
- Recursive processing of HTML pages found during crawling

## Installation

### Prerequisites

- Go 1.16 or higher
- git

### Building from Source

```bash
# Clone the repository
git clone https://github.com/sctg-development/wp-go-downloader.git
cd /wp-go-downloader

# Build the project
go build ./wp-go-downloader.go
```

## Usage

```bash
# Basic usage with default options
./wp-downloader

# Specify a custom WordPress site URL
./wp-downloader -url https://mywordpresssite.com
```

The tool will:

1. Download the initial HTML page
2. Parse and find all assets and links
3. Recursively process linked HTML pages
4. Download all referenced files (images, CSS, JS, etc.)
5. Process CSS files to extract and download referenced resources
6. Convert absolute URLs to relative for offline navigation
7. Save everything to a directory named after the site's domain

The resulting offline website will be saved in a folder with the domain name.

## How It Works

1. The tool fetches the HTML of the provided URL
2. It parses the HTML to extract all URLs (scripts, images, CSS, links, etc.)
3. It downloads all assets and processes them
4. For each HTML page found, it repeats the process recursively
5. It post-processes all HTML and CSS files to ensure proper offline functionality
6. It cleans up tracking scripts for improved privacy

## Limitations

- Does not handle dynamic content loaded via AJAX
- Cannot execute JavaScript
- Not designed for single-page applications (SPAs)
- Limited support for complex frontend frameworks

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

This project is licensed under the MIT License - see the [LICENSE.md](LICENSE.md) file for details.

## Credits

Created by Ronan Le Meillat.

This project was initially developed to download a specific WordPress website but has been generalized to work with most WordPress sites.
