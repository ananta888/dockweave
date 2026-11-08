// fake-provider is a local, deliberately insecure OpenAI-compatible test
// upstream for the credential broker demo. It prints the Authorization header
// it receives so the demo can prove that the broker injected the vault value.
// Never use it outside a local test.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
)

func main() {
	addr := envOr("FAKE_PROVIDER_ADDR", "127.0.0.1:18080")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("POST /chat/completions", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var req struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		_ = json.Unmarshal(body, &req)
		log.Printf("upstream request: model=%q auth=%q", req.Model, r.Header.Get("Authorization"))
		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			fl, _ := w.(http.Flusher)
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello from fake provider\"}}]}\n\n")
			if fl != nil {
				fl.Flush()
			}
			fmt.Fprint(w, "data: {\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":5}}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
			if fl != nil {
				fl.Flush()
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-demo", "object": "chat.completion", "model": req.Model,
			"choices": []map[string]any{{
				"index": 0, "finish_reason": "stop",
				"message": map[string]string{"role": "assistant", "content": "hello from fake provider"},
			}},
			"usage": map[string]int{"prompt_tokens": 12, "completion_tokens": 5},
		})
	})
	log.Printf("fake provider listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
