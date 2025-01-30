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
	"text/template"
)

type Config struct {
	OllamaURL      string      `json:"ollama_url"`
	Model          string      `json:"model"`
	PromptTemplate string      `json:"prompt_template"`
	Policies       []string    `json:"policies"`
	ResponseFormat interface{} `json:"response_format"`
}

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

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("error reading config file: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("error parsing config file: %w", err)
	}

	if cfg.OllamaURL == "" || cfg.Model == "" || cfg.PromptTemplate == "" {
		return nil, fmt.Errorf("missing required fields in config")
	}

	return &cfg, nil
}

func generatePrompt(message string, policies []string, promptTemplate string) (string, error) {
	tmpl, err := template.New("prompt").Funcs(template.FuncMap{
		"inc": func(i int) int { return i + 1 },
	}).Parse(promptTemplate)
	if err != nil {
		return "", fmt.Errorf("error parsing prompt template: %w", err)
	}

	data := struct {
		Message  string
		Policies []string
	}{
		Message:  message,
		Policies: policies,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("error executing template: %w", err)
	}

	return buf.String(), nil
}

func handleAnalyze(cfg *Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
			result, err := analyzeMessage(r.Context(), message, cfg)
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
}

func analyzeMessage(ctx context.Context, message string, cfg *Config) (analysisResult, error) {
	prompt, err := generatePrompt(message, cfg.Policies, cfg.PromptTemplate)
	if err != nil {
		return analysisResult{}, fmt.Errorf("error generating prompt: %w", err)
	}

	ollamaReq := ollamaGenerateRequest{
		Model:  cfg.Model,
		Prompt: prompt,
		Stream: false,
		Format: cfg.ResponseFormat,
	}

	reqBody, _ := json.Marshal(ollamaReq)
	url := fmt.Sprintf("%s/api/generate", cfg.OllamaURL)
	req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return analysisResult{}, fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	var ollamaResp struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return analysisResult{}, fmt.Errorf("error decoding response: %w", err)
	}

	var modResult moderationResult
	if err := json.Unmarshal([]byte(ollamaResp.Response), &modResult); err != nil {
		return analysisResult{}, fmt.Errorf("error parsing moderation result: %w", err)
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

func handleOllamaHealth(ollamaURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		url := fmt.Sprintf("%s/api/version", ollamaURL)
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
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
}

func main() {
	cfg, err := loadConfig("config.json")
	if err != nil {
		log.Fatalf("Error loading configuration: %v", err)
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Content moderation service is running"))
	})

	mux.HandleFunc("/api/health", corsMiddleware(handleOllamaHealth(cfg.OllamaURL)))
	mux.HandleFunc("/api/analyze", corsMiddleware(handleAnalyze(cfg)))

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
