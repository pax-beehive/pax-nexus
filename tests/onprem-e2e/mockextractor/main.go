package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

const listenAddress = ":8081"

func main() {
	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		if err := checkHealth(&http.Client{Timeout: time.Second}, "http://127.0.0.1:8081/healthz"); err != nil {
			log.Print(err)
			os.Exit(1)
		}
		return
	}
	server := newServer()
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("serve deterministic extractor: %v", err)
	}
}

func newServer() *http.Server {
	return &http.Server{
		Addr: listenAddress, Handler: newHandler(),
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second,
	}
}

func newHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("POST /v1/chat/completions", complete)
	return mux
}

func health(response http.ResponseWriter, _ *http.Request) {
	response.WriteHeader(http.StatusOK)
}

func complete(response http.ResponseWriter, request *http.Request) {
	if request.Header.Get("Authorization") != "Bearer e2e-extractor-key" {
		http.Error(response, "unauthorized", http.StatusUnauthorized)
		return
	}
	content := `{"candidates":[{"action":"create","kind":"status","subject":"July release approval decision","identity_ref":"decision/july-release-approval","body":"The team approved the July release with approval code ORBIT-731.","task_ref":"release-42","evidence_event_ids":["e2e-event-1"]}]}`
	response.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(response).Encode(map[string]any{
		"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": content}}},
		"usage":   map[string]int{"prompt_tokens": 32, "completion_tokens": 20},
	}); err != nil {
		log.Printf("encode deterministic extractor response: %v", err)
	}
}

func checkHealth(client *http.Client, endpoint string) error {
	response, err := client.Get(endpoint)
	if err != nil {
		return fmt.Errorf("check deterministic extractor health: %w", err)
	}
	if err := response.Body.Close(); err != nil {
		return fmt.Errorf("close deterministic extractor health response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("check deterministic extractor health: status %d", response.StatusCode)
	}
	return nil
}
