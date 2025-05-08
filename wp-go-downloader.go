// Copyright (c) 2025 Ronan Le Meillat
//
// Website Downloader - A tool for downloading Wordpress websites for offline viewing
//
// Author: Ronan Le Meillat
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"context"
	"log"

	"golang.org/x/net/html"
	"golang.org/x/sync/semaphore"
)

// Constants and global variables
const defaultURL = "https://www.example.com/"

var (
	// HTTP client with a timeout for requests
	client = &http.Client{
		Timeout: 30 * time.Second,
	}
	// Current working directory
	workspace, _ = os.Getwd()
	// Output folder based on domain name
	outputFolder string
	// Base domain for the site we're downloading
	baseDomain string
	// Base URL for the site we're downloading
	baseURL string
	// Map to track directory URLs (ending with /)
	directoryURLs = make(map[string]bool)
	// Maximum concurrent downloads
	maxConcurrency int
	// Include pattern regex
	includeRegex *regexp.Regexp
	// Exclude pattern regex
	excludeRegex *regexp.Regexp
	// Initialize loggers
	infoLog  *log.Logger
	errorLog *log.Logger
	debugLog *log.Logger
)

func initLoggers(verbose bool) {
	infoLog = log.New(os.Stdout, "INFO: ", log.Ldate|log.Ltime)
	errorLog = log.New(os.Stderr, "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile)

	if verbose {
		debugLog = log.New(os.Stdout, "DEBUG: ", log.Ldate|log.Ltime|log.Lshortfile)
	} else {
		debugLog = log.New(io.Discard, "", 0)
	}
}

// Extractor manages the process of downloading a website
type Extractor struct {
	URL           string          // Base URL of the website to download
	Doc           *html.Node      // Parsed HTML document
	ScrapedURLs   map[string]bool // Map of unique URLs to be downloaded
	ProcessedHTML map[string]bool // Map to track which HTML pages have been processed
	HTMLQueue     []string        // Queue of HTML files to process
}

// NewExtractor creates and initializes a new Extractor
func NewExtractor(url string) (*Extractor, error) {
	baseURL = url
	e := &Extractor{
		URL:           url,
		ScrapedURLs:   make(map[string]bool),
		ProcessedHTML: make(map[string]bool),
		HTMLQueue:     []string{url},
	}

	// Get and parse the HTML content
	content, err := e.GetPageContent(url)
	if err != nil {
		return nil, fmt.Errorf("failed to get page content: %v", err)
	}

	// Parse the HTML document
	doc, err := html.Parse(strings.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %v", err)
	}
	e.Doc = doc

	// Extract all URLs to be downloaded
	e.ScrapAllURLs()

	return e, nil
}

// Run performs the main execution flow - downloading files and saving HTML
func (e *Extractor) Run() error {
	// Process the initial page and any additional HTML pages recursively
	for len(e.HTMLQueue) > 0 {
		// Get the next URL to process
		currentURL := e.HTMLQueue[0]
		e.HTMLQueue = e.HTMLQueue[1:]

		// Skip if already processed
		if e.ProcessedHTML[currentURL] {
			continue
		}

		// Mark as processed
		e.ProcessedHTML[currentURL] = true

		// Only process if it's not the initial URL (which was already processed in NewExtractor)
		if currentURL != e.URL {
			infoLog.Printf("Processing additional HTML page: %s", currentURL)

			// Get and parse the HTML content
			content, err := e.GetPageContent(currentURL)
			if err != nil {
				errorLog.Printf("Error fetching %s: %v", currentURL, err)
				continue
			}

			// Parse the HTML document
			doc, err := html.Parse(strings.NewReader(content))
			if err != nil {
				errorLog.Printf("Error parsing HTML for %s: %v", currentURL, err)
				continue
			}

			// Temporarily store the current document
			originalDoc := e.Doc
			e.Doc = doc

			// Extract all URLs from this page
			e.ScrapAllURLs()

			// Restore the original document
			e.Doc = originalDoc

			// Convert URL to local path for storage
			localPath := URLToLocalPath(currentURL, false)
			outputPath := filepath.Join(workspace, outputFolder, localPath)

			// Make sure the directory exists
			if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
				errorLog.Printf("Failed to create directory for %s: %v", localPath, err)
				continue
			}

			// Save the modified HTML
			var buf bytes.Buffer
			if err := html.Render(&buf, doc); err != nil {
				errorLog.Printf("Failed to render HTML for %s: %v", currentURL, err)
				continue
			}

			// Write to file
			if err := os.WriteFile(outputPath, buf.Bytes(), 0644); err != nil {
				errorLog.Printf("Failed to write HTML file %s: %v", outputPath, err)
				continue
			}

			infoLog.Printf("Saved HTML file: %s", localPath)
		}
	}

	// Download all files
	if err := e.SaveFiles(); err != nil {
		return fmt.Errorf("failed to save files: %v", err)
	}

	// Save the modified HTML
	if err := e.SaveHTML(); err != nil {
		return fmt.Errorf("failed to save HTML: %v", err)
	}

	// Post-process HTML files to convert remaining absolute URLs to relative
	// and download any missed files
	missingFiles, err := PostProcessHTMLFiles()
	if err != nil {
		return fmt.Errorf("failed to post-process HTML files: %v", err)
	}

	// Process CSS files to extract and download referenced resources (fonts, images, etc.)
	cssFiles, err := ProcessCSSFiles()
	if err != nil {
		return fmt.Errorf("failed to process CSS files: %v", err)
	}

	// Combine all missing files
	for url, path := range cssFiles {
		missingFiles[url] = path
	}

	// Download any missing files found during post-processing
	if len(missingFiles) > 0 {
		infoLog.Printf("Downloading %d missing files found during post-processing...", len(missingFiles))
		if err := e.DownloadMissingFiles(missingFiles); err != nil {
			return fmt.Errorf("failed to download missing files: %v", err)
		}
	}

	return nil
}

// NormalizeWordPressURL removes cache-busting query parameters from WordPress URLs
// For example, wp-content/themes/kabuto/style.css?ver=6.6.2 becomes wp-content/themes/kabuto/style.css
func NormalizeWordPressURL(urlStr string) string {
	// Check if this is a JavaScript or CSS file with a cache-busting query parameter
	extensions := []string{".css", ".js", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".woff", ".woff2", ".ttf", ".eot"}
	hasQueryString := strings.Contains(urlStr, "?")

	if hasQueryString {
		for _, ext := range extensions {
			// Check if the URL has both the extension and a query string
			if strings.Contains(urlStr, ext+"?") {
				// Extract the base URL without query parameters
				baseURL := strings.Split(urlStr, "?")[0]
				return baseURL
			}
		}
	}

	// Return original URL if no changes needed
	return urlStr
}

// GetPageContent retrieves the HTML content of a URL
func (e *Extractor) GetPageContent(url string) (string, error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(body), nil
}

// ScrapAllURLs collects all URLs from scripts and assets
func (e *Extractor) ScrapAllURLs() {
	// Process scripts
	e.ScrapScripts()

	// Process assets (forms, links, images, etc.)
	e.ScrapAssets()
}

// IsExternalURL checks if a URL is external (from a different domain)
func IsExternalURL(urlStr string) bool {
	u, err := url.Parse(urlStr)
	if err != nil {
		return false
	}

	return u.Hostname() != baseDomain && u.Hostname() != "www."+baseDomain && baseDomain != "www."+u.Hostname()
}

// ScrapScripts extracts script URLs and modifies script tags to use local paths
func (e *Extractor) ScrapScripts() {
	// Find all script tags in the document
	var scriptNodes []*html.Node
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "script" {
			scriptNodes = append(scriptNodes, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(e.Doc)

	// Process each script tag
	for _, script := range scriptNodes {
		var scriptURL string
		// Look for src attribute
		for i, attr := range script.Attr {
			if attr.Key == "src" && attr.Val != "" {
				scriptURL = attr.Val

				// Convert relative URLs to absolute
				if !strings.HasPrefix(scriptURL, "http") {
					scriptURL = JoinURL(e.URL, scriptURL)
				}

				// Skip external scripts - keep URL as is
				if IsExternalURL(scriptURL) {
					infoLog.Printf("Keeping external script URL: %s", scriptURL)
					continue
				}

				// Normalize WordPress URLs
				normalizedURL := NormalizeWordPressURL(scriptURL)

				// Add to list of URLs to download (without query parameters)
				urlToDownload := strings.Split(normalizedURL, "?")[0]
				e.ScrapedURLs[urlToDownload] = true

				// Convert URL to local path format
				newURL := URLToLocalPath(scriptURL, true)
				if newURL != "" {
					// Update the src attribute to point to local file
					script.Attr[i].Val = newURL
				}
			}
		}
	}
}

// ScrapAssets combines all asset URLs from different elements (forms, links, images, etc.)
func (e *Extractor) ScrapAssets() {
	// Process different HTML elements
	e.ScrapForms()
	e.ScrapLinks()
	e.ScrapImages()
	e.ScrapStylesheets()
	e.ScrapButtons()
}

// ScrapForms extracts form action URLs and updates them to local paths
func (e *Extractor) ScrapForms() {
	var formNodes []*html.Node
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "form" {
			formNodes = append(formNodes, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(e.Doc)

	// Process each form tag
	for _, form := range formNodes {
		var formURL string
		for i, attr := range form.Attr {
			if attr.Key == "action" && attr.Val != "" {
				formURL = attr.Val

				// Convert relative URLs to absolute
				if !strings.HasPrefix(formURL, "http") {
					formURL = JoinURL(e.URL, formURL)
				}

				// Skip external URLs - keep URL as is
				if IsExternalURL(formURL) {
					infoLog.Printf("Keeping external form action URL: %s", formURL)
					continue
				}

				// Convert URL to local path format
				newURL := URLToLocalPath(formURL, true)
				if newURL != "" {
					// Update the action attribute
					form.Attr[i].Val = newURL
					// Add to list of URLs to download
					e.ScrapedURLs[strings.Split(formURL, "?")[0]] = true
				}
			}
		}
	}
}

// ScrapLinks extracts anchor href URLs and updates them to local paths
func (e *Extractor) ScrapLinks() {
	var linkNodes []*html.Node
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			linkNodes = append(linkNodes, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(e.Doc)

	// Process each anchor tag
	for _, link := range linkNodes {
		var linkURL string
		for i, attr := range link.Attr {
			if attr.Key == "href" && attr.Val != "" {
				linkURL = attr.Val

				// Convert relative URLs to absolute
				if !strings.HasPrefix(linkURL, "http") {
					linkURL = JoinURL(e.URL, linkURL)
				}

				// Skip external URLs - keep URL as is
				if IsExternalURL(linkURL) {
					continue
				}

				// Apply include/exclude patterns
				if !shouldProcessURL(linkURL) {
					continue
				}

				// Check if this is an HTML page we should process
				// Add HTML files to the queue for recursive processing
				if isHTMLPage(linkURL) && !e.ProcessedHTML[linkURL] {
					e.HTMLQueue = append(e.HTMLQueue, linkURL)
				}

				// Add to list of URLs to download
				urlToDownload := strings.Split(linkURL, "?")[0]
				e.ScrapedURLs[urlToDownload] = true

				// Convert URL to local path format
				newURL := URLToLocalPath(linkURL, true)
				if newURL != "" {
					// Update the href attribute
					link.Attr[i].Val = newURL
				}
			}
		}
	}
}

// ScrapImages extracts image src URLs and updates them to local paths
func (e *Extractor) ScrapImages() {
	var imgNodes []*html.Node
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "img" {
			imgNodes = append(imgNodes, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(e.Doc)

	// Process each image tag
	for _, img := range imgNodes {
		// Process src attribute
		for i, attr := range img.Attr {
			if attr.Key == "src" && attr.Val != "" {
				imgURL := attr.Val

				// Convert relative URLs to absolute
				if !strings.HasPrefix(imgURL, "http") {
					imgURL = JoinURL(e.URL, imgURL)
				}

				// Skip external URLs - keep URL as is
				if IsExternalURL(imgURL) {
					infoLog.Printf("Keeping external image URL: %s", imgURL)
					continue
				}

				// Convert URL to local path format
				newURL := URLToLocalPath(imgURL, true)
				if newURL != "" {
					// Update the src attribute
					img.Attr[i].Val = newURL
					// Add to list of URLs to download
					e.ScrapedURLs[strings.Split(imgURL, "?")[0]] = true
				}
			} else if attr.Key == "srcset" && attr.Val != "" {
				// Process srcset attribute (format: "url1 size1, url2 size2, ...")
				newSrcset, srcsetURLs := ProcessSrcset(attr.Val, e.URL)

				// Add all srcset URLs to the download list
				for _, srcsetURL := range srcsetURLs {
					// Skip external URLs
					if IsExternalURL(srcsetURL) {
						continue
					}
					// Add to list of URLs to download
					e.ScrapedURLs[strings.Split(srcsetURL, "?")[0]] = true
				}

				// Update the srcset attribute
				img.Attr[i].Val = newSrcset
			}
		}
	}
}

// ProcessSrcset handles the srcset attribute of images
// Returns the updated srcset string and a list of absolute URLs to download
func ProcessSrcset(srcset, baseURL string) (string, []string) {
	// Split the srcset by commas to get individual entries
	entries := strings.Split(srcset, ",")
	var processedEntries []string
	var urls []string

	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		// Split entry into URL and descriptor (e.g., "image.jpg 800w")
		parts := strings.Fields(entry)
		if len(parts) < 1 {
			continue
		}

		imgURL := parts[0]
		descriptor := ""
		if len(parts) > 1 {
			descriptor = parts[1]
		}

		// Convert relative URLs to absolute
		var absoluteURL string
		if !strings.HasPrefix(imgURL, "http") {
			absoluteURL = JoinURL(baseURL, imgURL)
			imgURL = absoluteURL // Update for local path conversion
		} else {
			absoluteURL = imgURL
		}

		// Skip external URLs
		if IsExternalURL(absoluteURL) {
			processedEntries = append(processedEntries, entry)
			continue
		}

		// Add to list of URLs to download
		urls = append(urls, absoluteURL)

		// Convert URL to local path format
		normalizedURL := NormalizeWordPressURL(imgURL)
		localPath := URLToLocalPath(normalizedURL, false)

		// Reconstruct the entry with the local path
		if descriptor != "" {
			processedEntries = append(processedEntries, localPath+" "+descriptor)
		} else {
			processedEntries = append(processedEntries, localPath)
		}
	}

	// Join processed entries back into a srcset string
	return strings.Join(processedEntries, ", "), urls
}

// ScrapStylesheets extracts link href URLs and updates them to local paths
func (e *Extractor) ScrapStylesheets() {
	var linkNodes []*html.Node
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "link" {
			linkNodes = append(linkNodes, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(e.Doc)

	// Process each link tag
	for _, link := range linkNodes {
		var linkURL string
		for i, attr := range link.Attr {
			if attr.Key == "href" && attr.Val != "" {
				linkURL = attr.Val

				// Convert relative URLs to absolute
				if !strings.HasPrefix(linkURL, "http") {
					linkURL = JoinURL(e.URL, linkURL)
				}

				// Skip external URLs - keep URL as is
				if IsExternalURL(linkURL) {
					infoLog.Printf("Keeping external stylesheet URL: %s", linkURL)
					continue
				}

				// Convert URL to local path format
				newURL := URLToLocalPath(linkURL, true)
				if newURL != "" {
					// Update the href attribute
					link.Attr[i].Val = newURL
					// Add to list of URLs to download
					e.ScrapedURLs[strings.Split(linkURL, "?")[0]] = true
				}
			}
		}
	}
}

// ScrapButtons extracts button onclick URLs and updates them to local paths
func (e *Extractor) ScrapButtons() {
	var btnNodes []*html.Node
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "button" {
			btnNodes = append(btnNodes, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(e.Doc)

	// Process each button tag
	for _, btn := range btnNodes {
		for i, attr := range btn.Attr {
			if attr.Key == "onclick" && attr.Val != "" {
				buttonURL := attr.Val

				// Parse URL from JavaScript onclick handler
				buttonURL = strings.ReplaceAll(buttonURL, " ", "")
				if idx := strings.Index(buttonURL, "location.href="); idx >= 0 {
					buttonURL = buttonURL[idx+len("location.href="):]
					// Remove quotes (single, double, backticks)
					buttonURL = strings.ReplaceAll(buttonURL, "'", "")
					buttonURL = strings.ReplaceAll(buttonURL, "\"", "")
					buttonURL = strings.ReplaceAll(buttonURL, "`", "")

					if buttonURL != "" {
						if !strings.HasPrefix(buttonURL, "http") {
							buttonURL = JoinURL(e.URL, buttonURL)
						}

						// Skip external URLs - keep URL as is
						if IsExternalURL(buttonURL) {
							continue
						}

						// Convert URL to local path format
						newURL := URLToLocalPath(buttonURL, true)
						if newURL != "" {
							// Update the onclick attribute
							btn.Attr[i].Val = newURL
							// Add to list of URLs to download
							e.ScrapedURLs[strings.Split(buttonURL, "?")[0]] = true
						}
					}
				}
			}
		}
	}
}

// SaveFiles downloads all files in the ScrapedURLs map
func (e *Extractor) SaveFiles() error {
	// Clean output directory first
	outputPath := filepath.Join(workspace, outputFolder)
	if err := os.RemoveAll(outputPath); err != nil {
		return fmt.Errorf("failed to clean output directory: %v", err)
	}

	// Use a semaphore to limit concurrency based on the maxConcurrency setting
	sem := semaphore.NewWeighted(int64(maxConcurrency))
	ctx := context.Background()

	var wg sync.WaitGroup
	errorsChan := make(chan error, len(e.ScrapedURLs))

	// Track progress
	total := len(e.ScrapedURLs)
	var counter int32

	infoLog.Printf("Downloading %d files with %d workers...", total, maxConcurrency)

	// Download each file concurrently
	for url := range e.ScrapedURLs {
		wg.Add(1)

		go func(url string) {
			defer wg.Done()

			// Acquire semaphore
			if err := sem.Acquire(ctx, 1); err != nil {
				errorsChan <- fmt.Errorf("failed to acquire semaphore: %v", err)
				return
			}
			defer sem.Release(1)

			// Convert URL to local path
			localPath := URLToLocalPath(url, false)
			if localPath == "" {
				return
			}

			outputPath := filepath.Join(workspace, outputFolder, localPath)

			// Download the file
			if err := e.DownloadFile(url, outputPath); err != nil {
				errorsChan <- fmt.Errorf("failed to download %s: %v", url, err)
				return
			}

			// Update progress counter
			atomic.AddInt32(&counter, 1)
			if counter%10 == 0 || counter == int32(total) {
				infoLog.Printf("Progress: %d/%d files downloaded (%.1f%%)", counter, total, float64(counter)/float64(total)*100)
			}
		}(url)
	}

	// Wait for all downloads to complete
	wg.Wait()
	close(errorsChan)

	// Report any errors
	errorCount := 0
	for err := range errorsChan {
		errorLog.Printf("Error: %v", err)
		errorCount++
	}

	infoLog.Printf("Download complete: %d files successfully downloaded, %d errors", total-errorCount, errorCount)

	return nil
}

// DownloadFile with retry logic
func (e *Extractor) DownloadFile(url string, outputPath string) error {
	maxRetries := 3
	retryDelay := 1 * time.Second

	// Remove query string from URL for downloading
	url = strings.Split(url, "?")[0]

	var err error
	for attempt := 0; attempt < maxRetries; attempt++ {
		err = e.downloadFileOnce(url, outputPath)
		if err == nil {
			return nil // Success
		}

		// If this wasn't the last attempt, sleep before retry
		if attempt < maxRetries-1 {
			infoLog.Printf("Retrying download of %s (attempt %d/%d)", url, attempt+1, maxRetries)
			time.Sleep(retryDelay)
			// Exponential backoff
			retryDelay *= 2
		}
	}

	return fmt.Errorf("failed after %d attempts: %v", maxRetries, err)
}

// Actual download implementation
func (e *Extractor) downloadFileOnce(url, outputPath string) error {
	// Check if this is a directory URL (ending with /) that we need to save as index.html
	if strings.HasSuffix(url, "/") {
		// Make sure we create the appropriate directory structure
		dirPath := filepath.Dir(outputPath)
		if err := os.MkdirAll(dirPath, 0755); err != nil {
			return fmt.Errorf("failed to create directory: %v", err)
		}

		// Get the HTML content for this directory URL
		resp, err := client.Get(url)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		// Check if the request was successful
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("bad status: %s", resp.Status)
		}

		// Create the output file
		out, err := os.Create(outputPath)
		if err != nil {
			return err
		}
		defer out.Close()

		// Copy the response body to the file
		_, err = io.Copy(out, resp.Body)
		if err != nil {
			return err
		}

		relPath, _ := filepath.Rel(workspace, outputPath)
		infoLog.Printf("Downloaded directory index to %s", relPath)

		return nil
	}

	// Regular file download
	fileName := path.Base(url)

	// Skip if filename is empty
	if fileName == "" {
		return fmt.Errorf("empty filename")
	}

	// Create output directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %v", err)
	}

	// Download the file
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Check if the request was successful
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	// Create the output file
	out, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer out.Close()

	// Copy the response body to the file
	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return err
	}

	relPath, _ := filepath.Rel(workspace, outputPath)
	infoLog.Printf("Downloaded %s to %s", fileName, relPath)

	return nil
}

// DownloadMissingFiles downloads files that were found during post-processing
// but were not downloaded in the initial pass
func (e *Extractor) DownloadMissingFiles(missingFiles map[string]string) error {
	for url, localPath := range missingFiles {
		outputPath := filepath.Join(workspace, outputFolder, localPath)

		// Download the file
		if err := e.DownloadFile(url, outputPath); err != nil {
			errorLog.Printf("Failed to download missing file %s: %v", url, err)
			continue
		}
	}

	return nil
}

// SaveHTML saves the modified HTML document to index.html in the output folder
func (e *Extractor) SaveHTML() error {
	// Create output directory if it doesn't exist
	outputPath := filepath.Join(workspace, outputFolder)
	if err := os.MkdirAll(outputPath, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %v", err)
	}

	// Path for the index.html file
	htmlPath := filepath.Join(outputPath, "index.html")

	// Create the file
	file, err := os.Create(htmlPath)
	if err != nil {
		return fmt.Errorf("failed to create index.html: %v", err)
	}
	defer file.Close()

	// Render the HTML document with proper formatting
	var buf bytes.Buffer
	if err := html.Render(&buf, e.Doc); err != nil {
		return fmt.Errorf("failed to render HTML: %v", err)
	}

	// Write the formatted HTML to the file
	if _, err := file.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("failed to write HTML to file: %v", err)
	}

	relPath, _ := filepath.Rel(workspace, htmlPath)
	infoLog.Printf("Saved index.html to %s", relPath)

	return nil
}

// JoinURL joins baseURL and relURL to create an absolute URL
func JoinURL(baseURL, relURL string) string {
	base, err := url.Parse(baseURL)
	if err != nil {
		return ""

	}

	rel, err := url.Parse(relURL)
	if err != nil {
		return ""
	}

	return base.ResolveReference(rel).String()
}

// URLToLocalPath converts a URL to a local file path
// If keepQuery is true, query parameters are preserved
func URLToLocalPath(urlStr string, keepQuery bool) string {
	// First normalize WordPress URLs by removing cache-busting parameters
	urlStr = NormalizeWordPressURL(urlStr)

	u, err := url.Parse(urlStr)
	if err != nil {
		return ""
	}

	// Extract path from URL
	newURL := u.Path

	// Add query parameters if requested and they still exist after normalization
	if keepQuery && u.RawQuery != "" {
		newURL += "?" + u.RawQuery
	}

	// Remove leading slashes for local path
	if strings.HasPrefix(newURL, "/") {
		newURL = newURL[1:]
	}

	// Check if the path ends with a slash (directory-style URL)
	if strings.HasSuffix(newURL, "/") {
		// Track this as a directory URL
		directoryURLs[urlStr] = true
		// Append index.html to the path
		newURL = newURL + "index.html"
	} else if newURL == "" || path.Base(newURL) == u.Host {
		// Handle root URL or URL that only contains the domain
		newURL = "index.html"
	}

	return newURL
}

// NormalizeURL handles URLs that end with a slash by replacing them with index.html
func NormalizeURL(urlStr string) string {
	// Check if this URL is known to be a directory URL
	if directoryURLs[urlStr] {
		if strings.HasSuffix(urlStr, "/") {
			return urlStr + "index.html"
		}
	}
	return urlStr
}

// PostProcessHTMLFiles walks through all HTML files in the output folder
// and converts any remaining absolute URLs to relative ones
func PostProcessHTMLFiles() (map[string]string, error) {
	infoLog.Println("Post-processing HTML files to convert absolute URLs to relative...")

	// Map to track missing files (URL -> local path)
	missingFiles := make(map[string]string)

	// Walk through all files in the output directory
	err := filepath.Walk(filepath.Join(workspace, outputFolder), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Only process HTML files
		if strings.HasSuffix(strings.ToLower(path), ".html") {
			foundMissing, err := ProcessHTMLFile(path, missingFiles)
			if err != nil {
				errorLog.Printf("Error processing %s: %v", path, err)
			}

			// Add any missing files found
			for url, path := range foundMissing {
				missingFiles[url] = path
			}
		}

		return nil
	})

	return missingFiles, err
}

// ProcessHTMLFile opens an HTML file, replaces absolute URLs with relative ones,
// and saves the modified content. It also identifies any missing files.
func ProcessHTMLFile(filePath string, missingFiles map[string]string) (map[string]string, error) {
	// Map to track missing files found in this HTML file
	localMissingFiles := make(map[string]string)

	// Read the HTML file
	content, err := os.ReadFile(filePath)
	if err != nil {
		return localMissingFiles, fmt.Errorf("failed to read file: %v", err)
	}

	// Get the relative path depth (how many directories up to go)
	relPath, err := filepath.Rel(filepath.Join(workspace, outputFolder), filepath.Dir(filePath))
	if err != nil {
		relPath = ""
	}

	// Count how many directories to go up
	depth := 0
	if relPath != "." && relPath != "" {
		depth = len(strings.Split(relPath, string(filepath.Separator)))
	}

	// Create the proper prefix for relative paths
	prefix := ""
	if depth > 0 {
		prefix = strings.Repeat("../", depth)
	}

	// Convert content to string for processing
	htmlContent := string(content)

	// Remove Google Tag Manager scripts
	htmlContent = RemoveGoogleTagManager(htmlContent)

	// Pattern to find srcset attributes
	srcsetPattern := regexp.MustCompile(`srcset=["'](.*?)["']`)

	// Process srcset attributes in already downloaded HTML files
	htmlContent = srcsetPattern.ReplaceAllStringFunc(htmlContent, func(match string) string {
		srcsetContent := srcsetPattern.FindStringSubmatch(match)[1]

		// Process each URL in the srcset
		entries := strings.Split(srcsetContent, ",")
		var processedEntries []string

		for _, entry := range entries {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}

			parts := strings.Fields(entry)
			if len(parts) < 1 {
				continue
			}

			imgURL := parts[0]
			descriptor := ""
			if len(parts) > 1 {
				descriptor = parts[1]
			}

			// Check if this is a domain URL or root-relative URL
			var relativePath string
			if strings.HasPrefix(imgURL, "http") && !IsExternalURL(imgURL) {
				// Domain URL
				parsedURL, err := url.Parse(imgURL)
				if err != nil {
					continue
				}

				path := parsedURL.Path
				if strings.HasPrefix(path, "/") {
					path = path[1:]
				}

				// Normalize WordPress URLs
				path = NormalizeWordPressURL(path)

				relativePath = AbsoluteToRelative(path, prefix)

				// Check if file exists and add to missing files if not
				localFilePath := filepath.Join(workspace, outputFolder, path)
				if _, err := os.Stat(localFilePath); os.IsNotExist(err) {
					originalURL := "https://" + baseDomain + "/" + path
					localMissingFiles[originalURL] = path
				}
			} else if strings.HasPrefix(imgURL, "/") {
				// Root-relative URL
				path := imgURL[1:]

				// Normalize WordPress URLs
				path = NormalizeWordPressURL(path)

				relativePath = AbsoluteToRelative(path, prefix)

				// Check if file exists and add to missing files if not
				localFilePath := filepath.Join(workspace, outputFolder, path)
				if _, err := os.Stat(localFilePath); os.IsNotExist(err) {
					originalURL := "https://" + baseDomain + "/" + path
					localMissingFiles[originalURL] = path
				}
			} else {
				// Already a relative path or external URL
				relativePath = imgURL
			}

			// Reconstruct entry with relative path
			if descriptor != "" {
				processedEntries = append(processedEntries, relativePath+" "+descriptor)
			} else {
				processedEntries = append(processedEntries, relativePath)
			}
		}

		// Rebuild srcset attribute
		return `srcset="` + strings.Join(processedEntries, ", ") + `"`
	})

	// Define URL patterns to replace
	// 1. Full URLs with domain: https://www.domain.com/path/ → ../path/index.html
	domainURLPattern := regexp.MustCompile(`(href|src|action)=["'](https?://` + regexp.QuoteMeta(baseDomain) + `|https?://www\.` + regexp.QuoteMeta(baseDomain) + `)(/?[^"'#]*)["']`)

	// 2. Root-relative URLs: /path/ → ../path/index.html
	rootRelativePattern := regexp.MustCompile(`(href|src|action)=["'](/[^"'#]*)["']`)

	// Replace domain URLs (https://www.domain.com/path/)
	htmlContent = domainURLPattern.ReplaceAllStringFunc(htmlContent, func(match string) string {
		parts := domainURLPattern.FindStringSubmatch(match)
		if len(parts) < 4 {
			return match
		}

		attr := parts[1] // href, src, action
		path := parts[3] // /path/

		// Skip if it's a external URL (already checked against our domain)
		if strings.HasPrefix(path, "//") {
			return match
		}

		// Normalize WordPress URLs by removing version query strings
		path = NormalizeWordPressURL(path)

		// Convert the path to a relative URL
		relativeURL := AbsoluteToRelative(path, prefix)

		return fmt.Sprintf(`%s="%s"`, attr, relativeURL)
	})

	// Replace root-relative URLs (/path/)
	htmlContent = rootRelativePattern.ReplaceAllStringFunc(htmlContent, func(match string) string {
		parts := rootRelativePattern.FindStringSubmatch(match)
		if len(parts) < 3 {
			return match
		}

		attr := parts[1] // href, src, action
		path := parts[2] // /path/

		// Skip if it's a external URL
		if strings.HasPrefix(path, "//") {
			return match
		}

		// Normalize WordPress URLs by removing version query strings
		path = NormalizeWordPressURL(path)

		// Convert the path to a relative URL
		relativeURL := AbsoluteToRelative(path, prefix)

		return fmt.Sprintf(`%s="%s"`, attr, relativeURL)
	})

	// Write the modified content back to the file
	err = os.WriteFile(filePath, []byte(htmlContent), os.ModePerm)
	if err != nil {
		return localMissingFiles, fmt.Errorf("failed to write file: %v", err)
	}

	relativePath, _ := filepath.Rel(filepath.Join(workspace, outputFolder), filePath)
	infoLog.Printf("Processed %s", relativePath)

	return localMissingFiles, nil
}

// RemoveGoogleTagManager removes Google Tag Manager script blocks from HTML content
func RemoveGoogleTagManager(htmlContent string) string {
	// Pattern for Google Tag Manager script in head
	gtmHeadPattern := regexp.MustCompile(`(?s)<!-- Google Tag Manager -->.*?<!-- End Google Tag Manager -->`)
	htmlContent = gtmHeadPattern.ReplaceAllString(htmlContent, "")

	// Pattern for Google Tag Manager noscript in body
	gtmBodyPattern := regexp.MustCompile(`(?s)<!-- Google Tag Manager \(noscript\) -->.*?<!-- End Google Tag Manager \(noscript\) -->`)
	htmlContent = gtmBodyPattern.ReplaceAllString(htmlContent, "")

	// Pattern for other Google Analytics scripts
	gaPattern := regexp.MustCompile(`<script .*src="https://www\.googletagmanager\.com/gtag/js\?id=[^"]+"></script>[\s\n]*<script>[\s\n]*window\.dataLayer[^<]*</script>`)
	htmlContent = gaPattern.ReplaceAllString(htmlContent, "")

	// Pattern for inline Google Analytics
	inlineGaPattern := regexp.MustCompile(`<script>[\s\n]*\(function\(i,s,o,g,r,a,m\)\{i\['GoogleAnalyticsObject'\][^<]*</script>`)
	htmlContent = inlineGaPattern.ReplaceAllString(htmlContent, "")

	// Pattern for Google reCaptcha
	recaptchaPattern := regexp.MustCompile(`(?s)<script .*src="https://www\.google\.com/recaptcha/api\.js\?render=[^"]+"></script>`)
	htmlContent = recaptchaPattern.ReplaceAllString(htmlContent, "")

	// Pattern for Google other reCaptcha with v parameter
	recaptchaV3Pattern := regexp.MustCompile(`(?s)<script .*src="https://www\.gstatic\.com/recaptcha/.*\.js\?v=[^"]+"></script>`)
	htmlContent = recaptchaV3Pattern.ReplaceAllString(htmlContent, "")

	// Pattern for Google reCaptcha from gstatic.com with various URL formats
	recaptchaGenericPattern := regexp.MustCompile(`<script[^>]*src="https://www\.gstatic\.com/recaptcha/[^"]*\.js"[^>]*></script>`)
	htmlContent = recaptchaGenericPattern.ReplaceAllString(htmlContent, "")

	// Pattern for any remaining Google reCaptcha scripts
	recaptchaAnyPattern := regexp.MustCompile(`<script[^>]*src="[^"]*recaptcha[^"]*\.js[^"]*"[^>]*></script>`)
	htmlContent = recaptchaAnyPattern.ReplaceAllString(htmlContent, "")

	return htmlContent
}

// AbsoluteToRelative converts an absolute path to a relative URL
// with proper handling of directories that should end with index.html
func AbsoluteToRelative(absolutePath string, prefix string) string {
	// Remove any leading slash
	if strings.HasPrefix(absolutePath, "/") {
		absolutePath = absolutePath[1:]
	}

	// Check if this path ends with a slash (directory-style URL)
	if strings.HasSuffix(absolutePath, "/") {
		return prefix + absolutePath + "index.html"
	}

	// Check if this is a known directory URL (stored without trailing slash)
	if directoryURLs[absolutePath] || directoryURLs["/"+absolutePath] {
		return prefix + absolutePath + "/index.html"
	}

	// Regular path, just add the prefix
	return prefix + absolutePath
}

// ProcessCSSFiles finds all downloaded CSS files and extracts URLs for fonts, images, etc.
func ProcessCSSFiles() (map[string]string, error) {
	infoLog.Println("Processing CSS files to extract referenced resources...")
	missingFiles := make(map[string]string)

	// Walk through all files in the output directory
	err := filepath.Walk(filepath.Join(workspace, outputFolder), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Only process CSS files
		if strings.HasSuffix(strings.ToLower(path), ".css") {
			foundResources, err := ExtractResourcesFromCSS(path)
			if err != nil {
				errorLog.Printf("Error processing CSS file %s: %v", path, err)
			} else {
				// Add found resources to the missing files map
				for url, localPath := range foundResources {
					missingFiles[url] = localPath
				}
			}
		}

		return nil
	})

	return missingFiles, err
}

// ExtractResourcesFromCSS parses a CSS file and extracts URLs of referenced resources
func ExtractResourcesFromCSS(cssFilePath string) (map[string]string, error) {
	resources := make(map[string]string)

	// Read the CSS file
	content, err := os.ReadFile(cssFilePath)
	if err != nil {
		return resources, fmt.Errorf("failed to read CSS file: %v", err)
	}

	cssContent := string(content)

	// Get the relative path depth for this CSS file
	relPath, err := filepath.Rel(filepath.Join(workspace, outputFolder), filepath.Dir(cssFilePath))
	if err != nil {
		relPath = ""
	}

	// Count how many directories up to go for relative paths
	depth := 0
	if relPath != "." && relPath != "" {
		depth = len(strings.Split(relPath, string(filepath.Separator)))
	}

	// Create proper prefix for relative paths
	prefix := ""
	if depth > 0 {
		prefix = strings.Repeat("../", depth)
	}

	// Use regular expressions to find URLs in the CSS
	// Pattern for url() references in CSS
	urlPattern := regexp.MustCompile(`url\s*\(\s*['"]?([^'"\)]+)['"]?\s*\)`)

	// Pattern for @import statements
	importPattern := regexp.MustCompile(`@import\s+(?:url\s*\(\s*['"]?([^'"\)]+)['"]?\s*\)|['"]([^'"]+)['"])`)

	// Find all url() references
	urlMatches := urlPattern.FindAllStringSubmatch(cssContent, -1)
	for _, match := range urlMatches {
		if len(match) >= 2 {
			urlRef := match[1]

			// Skip data URLs
			if strings.HasPrefix(urlRef, "data:") {
				continue
			}

			// Convert relative URLs to absolute
			var absoluteURL string
			if strings.HasPrefix(urlRef, "http") {
				absoluteURL = urlRef
			} else if strings.HasPrefix(urlRef, "/") {
				absoluteURL = "https://" + baseDomain + urlRef
			} else {
				// For relative paths, we need to determine the base URL
				cssRelativePath, _ := filepath.Rel(filepath.Join(workspace, outputFolder), cssFilePath)
				cssBasePath := filepath.Dir(cssRelativePath)

				// Join with the base URL
				absoluteURL = "https://" + baseDomain + "/" + filepath.ToSlash(filepath.Join(cssBasePath, urlRef))
			}

			// Skip external URLs
			if IsExternalURL(absoluteURL) {
				continue
			}

			// Normalize WordPress URLs
			absoluteURL = NormalizeWordPressURL(absoluteURL)

			// Extract the path part
			parsedURL, err := url.Parse(absoluteURL)
			if err != nil {
				continue
			}

			// Remove leading slash
			path := parsedURL.Path
			if strings.HasPrefix(path, "/") {
				path = path[1:]
			}

			// Add to resources map
			resources[absoluteURL] = path

			// Replace the URL in the CSS with a relative path
			relativeURL := AbsoluteToRelative(path, prefix)
			cssContent = strings.Replace(cssContent, match[0], "url("+relativeURL+")", -1)
		}
	}

	// Find all @import statements
	importMatches := importPattern.FindAllStringSubmatch(cssContent, -1)
	for _, match := range importMatches {
		var urlRef string
		if match[1] != "" {
			urlRef = match[1] // From url()
		} else if match[2] != "" {
			urlRef = match[2] // From direct quotes
		} else {
			continue
		}

		// Convert relative URLs to absolute
		var absoluteURL string
		if strings.HasPrefix(urlRef, "http") {
			absoluteURL = urlRef
		} else if strings.HasPrefix(urlRef, "/") {
			absoluteURL = "https://" + baseDomain + urlRef
		} else {
			// For relative paths
			cssRelativePath, _ := filepath.Rel(filepath.Join(workspace, outputFolder), cssFilePath)
			cssBasePath := filepath.Dir(cssRelativePath)
			absoluteURL = "https://" + baseDomain + "/" + filepath.ToSlash(filepath.Join(cssBasePath, urlRef))
		}

		// Skip external URLs
		if IsExternalURL(absoluteURL) {
			continue
		}

		// Normalize WordPress URLs
		absoluteURL = NormalizeWordPressURL(absoluteURL)

		// Extract the path part
		parsedURL, err := url.Parse(absoluteURL)
		if err != nil {
			continue
		}

		// Remove leading slash
		path := parsedURL.Path
		if strings.HasPrefix(path, "/") {
			path = path[1:]
		}

		// Add to resources map
		resources[absoluteURL] = path

		// Replace the import URL with a relative path
		relativeURL := AbsoluteToRelative(path, prefix)
		if match[1] != "" {
			cssContent = strings.Replace(cssContent, match[0], "@import url("+relativeURL+")", -1)
		} else {
			cssContent = strings.Replace(cssContent, match[0], "@import \""+relativeURL+"\"", -1)
		}
	}

	// Write the modified CSS content back to the file
	if err := os.WriteFile(cssFilePath, []byte(cssContent), 0644); err != nil {
		return resources, fmt.Errorf("failed to write updated CSS file: %v", err)
	}

	relativePath, _ := filepath.Rel(filepath.Join(workspace, outputFolder), cssFilePath)
	infoLog.Printf("Processed CSS file: %s (found %d resources)", relativePath, len(resources))

	return resources, nil
}

// isHTMLPage determines if a URL likely points to an HTML page
func isHTMLPage(urlStr string) bool {
	u, err := url.Parse(urlStr)
	if err != nil {
		return false
	}

	path := u.Path

	// URLs ending with / typically point to index.html
	if strings.HasSuffix(path, "/") {
		return true
	}

	// URLs with no file extension are typically HTML pages
	if !strings.Contains(path, ".") {
		return true
	}

	// URLs with .html, .htm extensions are HTML pages
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".html" || ext == ".htm" || ext == ".php" || ext == ""
}

// shouldProcessURL checks if a URL should be processed based on the include/exclude patterns
func shouldProcessURL(url string) bool {
	// If include pattern is set, URL must match it
	if includeRegex != nil && !includeRegex.MatchString(url) {
		return false
	}

	// If exclude pattern is set, URL must not match it
	if excludeRegex != nil && excludeRegex.MatchString(url) {
		return false
	}

	return true
}

func main() {
	// Parse command line arguments
	var (
		targetURL      string
		maxDepth       int
		concurrency    int
		customOutDir   string
		includePattern string
		excludePattern string
		timeout        int
		verbose        bool
	)

	flag.StringVar(&targetURL, "url", defaultURL, "URL to download")
	flag.IntVar(&maxDepth, "depth", 5, "Maximum crawl depth")
	flag.IntVar(&concurrency, "concurrency", 10, "Maximum concurrent downloads")
	flag.StringVar(&customOutDir, "outdir", "", "Custom output directory")
	flag.StringVar(&includePattern, "include", "", "Only include URLs matching this regex")
	flag.StringVar(&excludePattern, "exclude", "", "Exclude URLs matching this regex")
	flag.IntVar(&timeout, "timeout", 30, "HTTP timeout in seconds")
	flag.BoolVar(&verbose, "verbose", false, "Enable verbose logging")
	flag.Parse()

	initLoggers(verbose)
	// If URL provided as positional argument, use it
	args := flag.Args()
	if len(args) > 0 {
		targetURL = args[0]
	}

	// Set HTTP client timeout
	client.Timeout = time.Duration(timeout) * time.Second

	// Set maximum concurrency
	maxConcurrency = concurrency

	// Compile include/exclude regex patterns if provided
	if includePattern != "" {
		var err error
		includeRegex, err = regexp.Compile(includePattern)
		if err != nil {
			errorLog.Printf("Invalid include pattern: %v", err)
			os.Exit(1)
		}
		infoLog.Printf("Using include pattern: %s", includePattern)
	}

	if excludePattern != "" {
		var err error
		excludeRegex, err = regexp.Compile(excludePattern)
		if err != nil {
			errorLog.Printf("Invalid exclude pattern: %v", err)
			os.Exit(1)
		}
		infoLog.Printf("Using exclude pattern: %s", excludePattern)
	}

	// Extract domain name to use as output folder
	parsedURL, err := url.Parse(targetURL)
	if err != nil {
		errorLog.Printf("Invalid URL: %v", err)
		os.Exit(1)
	}

	// Set base domain for URL matching regardless of output folder
	baseDomain = parsedURL.Hostname()

	// Strip "www." prefix from base domain if present
	if strings.HasPrefix(baseDomain, "www.") {
		baseDomain = baseDomain[4:]
	}

	// Use custom output directory if provided, otherwise use domain name
	if customOutDir != "" {
		outputFolder = customOutDir
	} else {
		outputFolder = parsedURL.Hostname()
	}

	infoLog.Printf("Saving website to: %s", filepath.Join(workspace, outputFolder))

	// Create a new extractor
	infoLog.Printf("Extracting files from %s", targetURL)
	extractor, err := NewExtractor(targetURL)
	if err != nil {
		errorLog.Printf("Failed to create extractor: %v", err)
		os.Exit(1)
	}

	// Run the extraction process
	if err := extractor.Run(); err != nil {
		errorLog.Printf("Error during extraction: %v", err)
		os.Exit(1)
	}

	// Count the number of scraped URLs
	count := len(extractor.ScrapedURLs)
	infoLog.Printf("Total extracted files: %d", count)
}
