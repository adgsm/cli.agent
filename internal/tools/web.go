package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mdzunic/cli-agent/internal/ollama"
	"golang.org/x/net/html"
)

var webClient = &http.Client{Timeout: 15 * time.Second}

// FetchURLTool fetches a URL and returns readable text.
type FetchURLTool struct{}

func (t *FetchURLTool) NeedsApproval() bool { return false }

func (t *FetchURLTool) Definition() ollama.Tool {
	return ollama.Tool{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "fetch_url",
			Description: "Fetch a URL and return its text content (HTML is stripped to readable text).",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.Property{
					"url": {Type: "string", Description: "The URL to fetch."},
				},
				Required: []string{"url"},
			},
		},
	}
}

func (t *FetchURLTool) Execute(ctx context.Context, args string) (string, error) {
	m, err := UnmarshalArgs(args)
	if err != nil {
		return "", err
	}
	rawURL := stringArg(m, "url")
	if rawURL == "" {
		return "", fmt.Errorf("url is required")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; cli-agent/1.0)")

	resp, err := webClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d from %s", resp.StatusCode, rawURL)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB max
	if err != nil {
		return "", err
	}

	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "text/html") {
		text := extractText(string(body))
		if len(text) > 8000 {
			text = text[:8000] + "\n\n[truncated]"
		}
		return text, nil
	}
	result := string(body)
	if len(result) > 8000 {
		result = result[:8000] + "\n\n[truncated]"
	}
	return result, nil
}

// extractText strips HTML tags and returns readable text.
func extractText(htmlStr string) string {
	tokenizer := html.NewTokenizer(strings.NewReader(htmlStr))
	var sb strings.Builder
	skip := false

	for {
		tt := tokenizer.Next()
		switch tt {
		case html.ErrorToken:
			return strings.TrimSpace(sb.String())
		case html.StartTagToken, html.SelfClosingTagToken:
			tag, _ := tokenizer.TagName()
			tagName := string(tag)
			if tagName == "script" || tagName == "style" || tagName == "noscript" {
				skip = true
			}
		case html.EndTagToken:
			tag, _ := tokenizer.TagName()
			tagName := string(tag)
			if tagName == "script" || tagName == "style" || tagName == "noscript" {
				skip = false
			}
			if tagName == "p" || tagName == "div" || tagName == "br" || tagName == "h1" ||
				tagName == "h2" || tagName == "h3" || tagName == "li" {
				sb.WriteString("\n")
			}
		case html.TextToken:
			if !skip {
				text := strings.TrimSpace(string(tokenizer.Text()))
				if text != "" {
					sb.WriteString(text)
					sb.WriteString(" ")
				}
			}
		}
	}
}

// WebSearchTool searches DuckDuckGo by scraping the HTML results page.
type WebSearchTool struct{}

func (t *WebSearchTool) NeedsApproval() bool { return false }

func (t *WebSearchTool) Definition() ollama.Tool {
	return ollama.Tool{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "web_search",
			Description: "Search the web using DuckDuckGo and return a list of results with titles, URLs, and snippets.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.Property{
					"query":       {Type: "string", Description: "The search query."},
					"num_results": {Type: "integer", Description: "Number of results to return (default: 5, max: 10)."},
				},
				Required: []string{"query"},
			},
		},
	}
}

type searchResult struct {
	Title   string
	URL     string
	Snippet string
}

func (t *WebSearchTool) Execute(ctx context.Context, args string) (string, error) {
	m, err := UnmarshalArgs(args)
	if err != nil {
		return "", err
	}
	query := stringArg(m, "query")
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	n := min(intArg(m, "num_results", 5), 10)

	searchURL := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")

	resp, err := webClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("search failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("search returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 500_000))
	if err != nil {
		return "", err
	}

	results := parseDDGResults(string(body), n)

	if len(results) == 0 {
		return fmt.Sprintf("No results found for: %s\nTip: try fetch_url with a specific URL for more targeted research.", query), nil
	}

	var sb strings.Builder
	for i, r := range results {
		fmt.Fprintf(&sb, "%d. %s\n   URL: %s\n   %s\n\n", i+1, r.Title, r.URL, r.Snippet)
	}
	return sb.String(), nil
}

// parseDDGResults extracts search results from DuckDuckGo's HTML response.
func parseDDGResults(htmlStr string, maxResults int) []searchResult {
	var results []searchResult
	tokenizer := html.NewTokenizer(strings.NewReader(htmlStr))

	var inResult bool
	var inTitle bool
	var inSnippet bool
	var current searchResult

	for {
		tt := tokenizer.Next()
		switch tt {
		case html.ErrorToken:
			// Flush last result
			if inResult && current.Title != "" && len(results) < maxResults {
				results = append(results, current)
			}
			return results

		case html.StartTagToken, html.SelfClosingTagToken:
			tag, _ := tokenizer.TagName()
			tagName := string(tag)

			// Collect all attributes for this tag.
			attrs := map[string]string{}
			for {
				k, v, more := tokenizer.TagAttr()
				attrs[string(k)] = string(v)
				if !more {
					break
				}
			}

			switch {
			case tagName == "a" && !inResult && strings.Contains(attrs["class"], "result__a"):
				inResult = true
				inTitle = true
				current = searchResult{URL: cleanDDGURL(attrs["href"])}
			case tagName == "td" && strings.Contains(attrs["class"], "result__snippet"):
				inSnippet = true
			case tagName == "div" && inResult && strings.Contains(attrs["class"], "result") &&
				strings.Contains(attrs["class"], "links") && strings.Contains(attrs["class"], "main"):
				if current.Title != "" && len(results) < maxResults {
					results = append(results, current)
				}
				inResult = false
				inTitle = false
				inSnippet = false
			}

		case html.EndTagToken:
			tag, _ := tokenizer.TagName()
			switch string(tag) {
			case "a":
				if inTitle {
					inTitle = false
				}
			case "td":
				if inSnippet {
					inSnippet = false
					if current.Title != "" && len(results) < maxResults {
						results = append(results, current)
						inResult = false
					}
				}
			}

		case html.TextToken:
			text := string(tokenizer.Text())
			if inTitle {
				current.Title += text
			} else if inSnippet {
				current.Snippet += text
			}
		}
	}
}

// cleanDDGURL extracts the actual URL from DDG's redirect URL.
func cleanDDGURL(href string) string {
	// DDG links look like //duckduckgo.com/l/?uddg=https%3A%2F%2F...
	if strings.Contains(href, "uddg=") {
		if u, err := url.Parse(href); err == nil {
			if encoded := u.Query().Get("uddg"); encoded != "" {
				if decoded, err := url.QueryUnescape(encoded); err == nil {
					return decoded
				}
			}
		}
	}
	return href
}
