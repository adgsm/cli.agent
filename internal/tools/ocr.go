package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/mdzunic/cli-agent/internal/ollama"
)

const maxImageBytes = 10 * 1024 * 1024 // 10MB

var imageExtensions = map[string]bool{
	".png":  true,
	".jpg":  true,
	".jpeg": true,
	".gif":  true,
	".webp": true,
	".bmp":  true,
	".tiff": true,
	".tif":  true,
	".pdf":  true,
}

// OCRTool reads text from images using an OCR model via Ollama.
type OCRTool struct {
	Client     *http.Client
	OllamaURL  string
	OCRModel   string // model to use for OCR (e.g. "glm-ocr:bf16")
}

func (t *OCRTool) NeedsApproval() bool { return false }

func (t *OCRTool) Definition() ollama.Tool {
	return ollama.Tool{
		Type: "function",
		Function: ollama.ToolFunction{
			Name:        "read_image",
			Description: "Read/extract text from an image file using OCR. Supports PNG, JPG, GIF, WebP, BMP, TIFF, and PDF files.",
			Parameters: ollama.ToolParameters{
				Type: "object",
				Properties: map[string]ollama.Property{
					"path": {Type: "string", Description: "Path to the image file to read."},
				},
				Required: []string{"path"},
			},
		},
	}
}

func (t *OCRTool) Execute(ctx context.Context, args string) (string, error) {
	m, err := UnmarshalArgs(args)
	if err != nil {
		return "", err
	}
	path := stringArg(m, "path")
	if path == "" {
		return "", fmt.Errorf("path is required")
	}

	// Validate file extension.
	dotIdx := strings.LastIndex(path, ".")
	if dotIdx == -1 {
		return "", fmt.Errorf("file has no extension, cannot determine image type")
	}
	ext := strings.ToLower(path[dotIdx:])
	if !imageExtensions[ext] {
		return "", fmt.Errorf("unsupported image format: %s (supported: png, jpg, jpeg, gif, webp, bmp, tiff, pdf)", ext)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading image: %w", err)
	}
	if len(data) > maxImageBytes {
		return "", fmt.Errorf("image too large: %d bytes (max %d)", len(data), maxImageBytes)
	}

	if t.OllamaURL == "" {
		t.OllamaURL = "http://localhost:11434"
	}
	if t.OCRModel == "" {
		t.OCRModel = "glm-ocr:bf16"
	}

	encoded := base64.StdEncoding.EncodeToString(data)

	// Build a non-streaming chat request with the image.
	reqBody := map[string]any{
		"model": t.OCRModel,
		"messages": []map[string]any{
			{
				"role":    "user",
				"content": "Extract all text from this image. Return only the extracted text, nothing else.",
				"images":  []string{encoded},
			},
		},
		"stream": false,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.OllamaURL+"/api/chat", strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	client := t.Client
	if client == nil {
		client = &http.Client{}
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("OCR request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errBody struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		return "", fmt.Errorf("OCR model returned %d: %s", resp.StatusCode, errBody.Error)
	}

	var chatResp struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return "", fmt.Errorf("decoding OCR response: %w", err)
	}

	result := strings.TrimSpace(chatResp.Message.Content)
	if result == "" {
		return "(no text detected in image)", nil
	}
	return result, nil
}
