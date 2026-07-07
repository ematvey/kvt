package embed

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestOpenAICompatibleEmbedsTexts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			http.NotFound(w, r)
			return
		}
		defer r.Body.Close()

		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req["model"] != "test-model" {
			t.Fatalf("model = %#v", req["model"])
		}
		if !reflect.DeepEqual(req["input"], []any{"alpha", "beta"}) {
			t.Fatalf("input = %#v", req["input"])
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"index": 0, "embedding": []float32{1, 0}},
				{"index": 1, "embedding": []float32{0, 1}},
			},
		})
	}))
	defer server.Close()

	client := NewOpenAICompatible(server.URL, "test-model", "", 2)
	got, err := client.Embed(t.Context(), []string{"alpha", "beta"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	want := [][]float32{{1, 0}, {0, 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("vectors = %#v, want %#v", got, want)
	}
}

func TestOpenAICompatibleRejectsShortEmbeddingResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"index": 0, "embedding": []float32{1, 0}},
			},
		})
	}))
	defer server.Close()

	client := NewOpenAICompatible(server.URL, "test-model", "", 2)
	_, err := client.Embed(t.Context(), []string{"alpha", "beta"})
	if err == nil {
		t.Fatalf("expected short embedding response error")
	}
}

func TestOllamaEmbedsTexts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			http.NotFound(w, r)
			return
		}
		defer r.Body.Close()

		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req["model"] != "nomic-embed-text" {
			t.Fatalf("model = %#v", req["model"])
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"embeddings": []any{
				[]float32{0.25, 0.75},
				[]float32{0.5, 0.5},
			},
		})
	}))
	defer server.Close()

	client := NewOllama(server.URL, "nomic-embed-text", 2)
	got, err := client.Embed(t.Context(), []string{"alpha", "beta"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	want := [][]float32{{0.25, 0.75}, {0.5, 0.5}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("vectors = %#v, want %#v", got, want)
	}
}

func TestOllamaRejectsShortEmbeddingResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embeddings": []any{
				[]float32{0.25, 0.75},
			},
		})
	}))
	defer server.Close()

	client := NewOllama(server.URL, "nomic-embed-text", 2)
	_, err := client.Embed(t.Context(), []string{"alpha", "beta"})
	if err == nil {
		t.Fatalf("expected short embedding response error")
	}
}
