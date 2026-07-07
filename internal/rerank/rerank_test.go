package rerank

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAICompatibleReranksCandidates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		defer r.Body.Close()

		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req["model"] != "gpt-test" {
			t.Fatalf("model = %#v", req["model"])
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"content": `[{"index":1,"score":0.91},{"index":0,"score":0.62}]`,
					},
				},
			},
		})
	}))
	defer server.Close()

	client := NewOpenAICompatible(server.URL, "gpt-test", "")
	got, err := client.Rerank(t.Context(), "who owns the database", []Candidate{
		{DocPath: "people/alice.md", Text: "Alice owns the backup cluster."},
		{DocPath: "people/bob.md", Text: "Bob owns the primary database."},
	})
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(scores) = %d", len(got))
	}
	if got[0].Index != 1 || got[0].Score <= got[1].Score {
		t.Fatalf("scores = %#v", got)
	}
}
