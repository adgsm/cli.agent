package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const defaultBaseURL = "http://localhost:11434"

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(baseURL string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 0, // no timeout; streaming responses can be long
		},
	}
}

// ListModels returns all locally available models.
func (c *Client) ListModels(ctx context.Context) ([]ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama unreachable at %s: %w", c.baseURL, err)
	}
	defer resp.Body.Close()

	var tags TagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return nil, err
	}
	return tags.Models, nil
}

// StreamCallback receives content tokens and the final done response.
// When done is true, chunk contains the last message (may have tool_calls).
type StreamCallback func(token string, chunk ChatResponse, done bool)

// Chat sends a chat request and streams the response, calling cb for each chunk.
// The final call has done=true and a fully assembled message (content + tool_calls).
func (c *Client) Chat(ctx context.Context, req ChatRequest, cb StreamCallback) error {
	req.Stream = true

	body, err := json.Marshal(req)
	if err != nil {
		return err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Use a separate client without timeout for streaming.
	streamClient := &http.Client{Timeout: 0}
	resp, err := streamClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("ollama chat failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errBody struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		return fmt.Errorf("ollama returned %d: %s", resp.StatusCode, errBody.Error)
	}

	// Accumulate content and tool_calls across all stream chunks.
	// Ollama streams tool_calls in a done=false chunk and content tokens separately.
	var sb strings.Builder
	var accToolCalls []ToolCall
	var finalChunk ChatResponse

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var chunk ChatResponse
		if err := json.Unmarshal(line, &chunk); err != nil {
			continue
		}

		if !chunk.Done {
			if len(chunk.Message.ToolCalls) > 0 {
				accToolCalls = append(accToolCalls, chunk.Message.ToolCalls...)
			}
			if chunk.Message.Content != "" {
				sb.WriteString(chunk.Message.Content)
				cb(chunk.Message.Content, chunk, false)
			}
		} else {
			finalChunk = chunk
			// Attach accumulated content and tool_calls to the final message.
			if finalChunk.Message.Content == "" {
				finalChunk.Message.Content = sb.String()
			}
			if len(finalChunk.Message.ToolCalls) == 0 && len(accToolCalls) > 0 {
				finalChunk.Message.ToolCalls = accToolCalls
			}
			cb("", finalChunk, true)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("stream read error: %w", err)
	}
	return nil
}

// Ping checks if Ollama is reachable.
func (c *Client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ollama not reachable at %s: %w", c.baseURL, err)
	}
	resp.Body.Close()
	return nil
}

// ModelInfo returns details about a model including its context length.
func (c *Client) ModelInfo(ctx context.Context, name string) (*ShowResponse, error) {
	body, _ := json.Marshal(map[string]string{"name": name})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/show", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("model info failed: %w", err)
	}
	defer resp.Body.Close()

	var show ShowResponse
	if err := json.NewDecoder(resp.Body).Decode(&show); err != nil {
		return nil, fmt.Errorf("model info decode: %w", err)
	}
	return &show, nil
}
