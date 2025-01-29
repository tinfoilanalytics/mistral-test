package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
)

const (
	modelName = "mistral"
	ollamaURL = "http://localhost:11434"
)

type analyzeRequest struct {
	Messages []string `json:"messages"`
}

type analysisResult struct {
	Content          string   `json:"content"`
	IsSafe           bool     `json:"is_safe"`
	ViolatedPolicies []string `json:"violated_policies"`
}

type ollamaGenerateRequest struct {
	Model  string      `json:"model"`
	Prompt string      `json:"prompt"`
	Stream bool        `json:"stream"`
	Format interface{} `json:"format"`
}

type moderationResult struct {
	Safe             bool     `json:"safe"`
	ViolatedPolicies []string `json:"violated_policies"`
}

func generatePrompt(message string) string {
	return fmt.Sprintf(`You are a content moderation system. Follow these policies:
1. Hate/harassment is disallowed.
2. Sexual content is disallowed.
3. Self-harm encouragement is disallowed.
4. Violence/graphic content is disallowed.

User input: "%s"

Return a JSON object with {"safe": boolean, "violated_policies": string[]}.`, message)
}

func handleAnalyze(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req analyzeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if len(req.Messages) == 0 {
		http.Error(w, "Messages array cannot be empty", http.StatusBadRequest)
		return
	}

	results := make([]analysisResult, 0, len(req.Messages))
	for _, message := range req.Messages {
		result, err := analyzeMessage(r.Context(), message)
		if err != nil {
			log.Printf("Error analyzing message '%s': %v", message, err)
			continue
		}
		results = append(results, result)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(results); err != nil {
		log.Printf("Error encoding response: %v", err)
	}
}

func analyzeMessage(ctx context.Context, message string) (analysisResult, error) {
	ollamaReq := ollamaGenerateRequest{
		Model:  "llama3.2:1b",
		Prompt: generatePrompt(message),
		Stream: false,
		Format: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"safe": map[string]string{"type": "boolean"},
				"violated_policies": map[string]interface{}{
					"type":  "array",
					"items": map[string]string{"type": "string"},
				},
			},
			"required": []string{"safe", "violated_policies"},
		},
	}

	reqBody, _ := json.Marshal(ollamaReq)
	req, _ := http.NewRequestWithContext(ctx, "POST", "http://localhost:11434/api/generate", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return analysisResult{}, fmt.Errorf("API request failed")
	}
	defer resp.Body.Close()

	var ollamaResp struct {
		Response string `json:"response"`
	}
	json.NewDecoder(resp.Body).Decode(&ollamaResp)

	var modResult moderationResult
	if err := json.Unmarshal([]byte(ollamaResp.Response), &modResult); err != nil {
		return analysisResult{}, fmt.Errorf("invalid JSON response")
	}

	return analysisResult{
		Content:          message,
		IsSafe:           modResult.Safe,
		ViolatedPolicies: modResult.ViolatedPolicies,
	}, nil
}

func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next(w, r)
	}
}

func handleOllamaHealth(w http.ResponseWriter, r *http.Request) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, ollamaURL+"/api/version", nil)
	if err != nil {
		http.Error(w, fmt.Sprintf("creating version request: %v", err), http.StatusServiceUnavailable)
		return
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, fmt.Sprintf("connecting to Ollama: %v", err), http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		http.Error(w, fmt.Sprintf("unexpected version status code %d: %s", resp.StatusCode, body), http.StatusBadGateway)
		return
	}

	w.Write([]byte("ollama: "))
	if _, err := io.Copy(w, resp.Body); err != nil {
		log.Printf("Error copying response: %v", err)
	}
}

func main() {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Content moderation service is running"))
	})

	mux.HandleFunc("/api/health", corsMiddleware(handleOllamaHealth))
	mux.HandleFunc("/api/analyze", corsMiddleware(handleAnalyze))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	addr := ":" + port

	log.Printf("Server starting on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
