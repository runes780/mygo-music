package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"mygo-music/internal/music"
)

func songExplainer(t *testing.T) *Plugin {
	t.Helper()
	p, err := Load(filepath.Join("..", "..", "examples", "song-explainer"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

// The provider example defaults to DeepSeek Flash and English, and can switch
// its endpoint, model and output language through settings alone.
func TestSongExplainerProviders(t *testing.T) {
	models := make(chan string, 2)
	languages := make(chan string, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		models <- body.Model
		var request struct {
			Language string `json:"language"`
		}
		json.Unmarshal([]byte(body.Messages[len(body.Messages)-1].Content), &request)
		languages <- request.Language
		content, _ := json.Marshal(map[string]any{
			"title": "Umbra", "summary": "A careful answer", "genres": []string{"Modern jazz"},
			"sections": []map[string]any{{"title": "How to listen", "body": "Follow the bass."}},
		})
		json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"content": string(content)}}}})
	}))
	defer srv.Close()

	p := songExplainer(t)
	p.SetSetting("baseURL", srv.URL)
	p.SetSetting("apiKey", "test")
	req := music.ExplanationRequest{Track: music.Track{Name: "Umbra", Artists: []music.Artist{{Name: "GoGo Penguin"}}}}
	answer, err := p.Explain(req)
	if err != nil {
		t.Fatal(err)
	}
	if model := <-models; model != "deepseek-flash" {
		t.Errorf("default model %q", model)
	}
	if language := <-languages; language != "English" {
		t.Errorf("default language %q", language)
	}
	if answer.Title != "Umbra" || len(answer.Sections) != 1 {
		t.Errorf("answer %+v", answer)
	}

	p.SetSetting("model", "another-model")
	p.SetSetting("outputLanguage", "zh-CN")
	if _, err := p.Explain(req); err != nil {
		t.Fatal(err)
	}
	if model := <-models; model != "another-model" {
		t.Errorf("custom model %q", model)
	}
	if language := <-languages; language != "Simplified Chinese" {
		t.Errorf("custom language %q", language)
	}
}

// DeepSeek without its key sends the listener to the plugin's settings before
// making a request.
func TestSongExplainerNeedsDeepSeekKey(t *testing.T) {
	p := songExplainer(t)
	_, err := p.Explain(music.ExplanationRequest{Track: music.Track{Name: "Umbra"}})
	if !errors.Is(err, music.ErrSetup) {
		t.Fatalf("got %v", err)
	}
}

// The provider publishes partial structured answers as SSE content arrives,
// then returns the complete answer with the same sections.
func TestSongExplainerStreams(t *testing.T) {
	streaming := make(chan bool, 1)
	content := `{"title":"Umbra","summary":"A careful answer","sections":[{"title":"How to listen","body":"Follow the bass."}]}`
	pieces := []string{`{"title":"Umbra"`, content[len(`{"title":"Umbra"`):60], content[60:]}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Stream bool `json:"stream"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		streaming <- body.Stream
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, piece := range pieces {
			event, _ := json.Marshal(map[string]any{"choices": []map[string]any{{"delta": map[string]any{"content": piece}}}})
			fmt.Fprintf(w, "data: %s\n\n", event)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	p := songExplainer(t)
	p.SetSetting("baseURL", srv.URL)
	p.SetSetting("apiKey", "test")
	var partials []music.Explanation
	answer, err := p.ExplainStream(music.ExplanationRequest{Track: music.Track{Name: "Umbra"}}, func(partial music.Explanation) {
		partials = append(partials, partial)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !<-streaming {
		t.Error("the request didn't ask the provider to stream")
	}
	if len(partials) == 0 || partials[0].Title != "Umbra" {
		t.Errorf("partial answers %+v", partials)
	}
	if answer.Summary != "A careful answer" || len(answer.Sections) != 1 {
		t.Errorf("final answer %+v", answer)
	}
}
