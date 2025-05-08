# WordPress Website Downloader

A robust Go-based tool for downloading WordPress websites for offline viewing. This project creates a complete offline copy of WordPress websites while maintaining structure, assets, and navigation.

## Disclaimer

This tool was originally designed for downloading a specific WordPress website but has been generalized to work with many WordPress sites.  
The downloader may need customization for specific websites.  
**Important:** Only use this tool on websites you own or have explicit permission to download. Always respect terms of service and copyright.

## Star the project

**If you find this tool useful, please consider giving it a star! 🤩**

## Features

- Complete website downloads with all assets (images, CSS, JavaScript, fonts)
- CSS processing to extract and download referenced resources
- WordPress-specific URL pattern handling and cache-busting parameter removal
- Multilingual site support with proper language directory handling
- Tracking script removal (Google Analytics, Google Tag Manager)
- Absolute to relative URL conversion for offline navigation
- Original directory structure preservation
- Support for responsive images (`srcset` attributes)
- Recursive HTML page processing
- **Configurable concurrent downloads**
- **URL filtering with include/exclude patterns**
- **Automatic retry on download failures**
- **Custom output directory support**
- **Adjustable HTTP timeout settings**
- **Detailed logging options**

## Installation

### Prerequisites

- Go 1.20 or higher
- Git

### Building from Source

```bash
# Clone the repository
git clone https://github.com/sctg-development/wp-go-downloader.git
cd wp-go-downloader

# Build the project
go build ./wp-go-downloader.go
```

## Usage

### Basic Usage

```bash
# With default options (downloads example.com)
./wp-go-downloader

# Specify a custom WordPress site URL
./wp-go-downloader -url https://mywordpresssite.com
```

### Advanced Options

```bash
# Set concurrency, output directory, and timeout
./wp-go-downloader -url https://mywordpresssite.com -concurrency 15 -outdir my-site-backup -timeout 60

# Filter URLs to download only specific files
./wp-go-downloader -url https://mywordpresssite.com -include "\.(jpg|png|gif)$" -exclude "/wp-admin/"
```

### Command Line Reference

| Option | Description | Default |
|--------|-------------|---------|
| `-url` | Website URL to download | https://www.example.com/ |
| `-concurrency` | Maximum parallel downloads | 10 |
| `-outdir` | Custom output directory | Domain name of the website |
| `-include` | Only process URLs matching this regex | None (process all URLs) |
| `-exclude` | Skip URLs matching this regex | None (process all URLs) |
| `-timeout` | HTTP request timeout in seconds | 30 |
| `-verbose` | Enable detailed logging | false |

## Process Overview

The tool performs the following steps:

1. Downloads the initial HTML page
2. Parses HTML to extract all URLs (scripts, images, CSS, links)
3. Downloads all assets concurrently within the concurrency limit
4. Recursively processes any HTML pages found during download
5. Post-processes HTML and CSS files for offline functionality
6. Removes tracking scripts and optimizes files
7. Saves the complete website to the specified output directory

## Limitations

- No support for content loaded dynamically via AJAX
- Cannot execute JavaScript to render dynamic content
- Not compatible with single-page applications (SPAs)
- Limited functionality with complex frontend frameworks
- May require adjustments for heavily customized WordPress sites

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

This tool was designed for specific use cases and may need adjustments for different WordPress installations. You're welcome to open Pull Requests to improve the tool or add new features to make it more versatile.

## License

This project is licensed under the MIT License - see the LICENSE.md file for details.

## Credits

Created by Ronan Le Meillat.
