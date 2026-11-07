package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"mygo-music/internal/lyrics"
	"mygo-music/internal/music"
)

// The Explain tab asks for the song playing only once it opens, and a follow-up
// sends the question with that song rather than starting another explanation.
func TestExplainSongAndFollowUp(t *testing.T) {
	a := newTestApp()
	ch := posts(a)
	asked := make(chan music.ExplanationRequest, 4)
	fake(a).explain = func(req music.ExplanationRequest) (*music.Explanation, error) {
		asked <- req
		summary := req.Question
		if summary == "" {
			summary = "Overview of " + req.Track.Name
		}
		return &music.Explanation{Title: req.Track.Name, Summary: summary, Sections: []music.ExplanationSection{{Title: "How to listen"}}}, nil
	}
	tr := song("7", "Umbra", "GoGo Penguin")
	a.pl.Cue([]music.Track{tr})
	a.pl.SetLyrics(nil)
	a.nowPlaying = true
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if len(asked) > 0 {
		t.Fatal("asked before the pane opened")
	}
	if err := tt.Click("Explain"); err != nil {
		t.Fatal(err)
	}
	req := wait(t, asked, "the song explanation")
	if req.Track.ID != "7" || req.Track.Name != "Umbra" || req.Lyric != "" || req.Question != "" {
		t.Fatalf("asked with %+v", req)
	}
	until(t, ch, func() bool { return !a.explanation.loading })
	tt.Frame()
	if _, ok := tt.Find("Ask about this song…"); ok {
		t.Fatal("the composer shows before the listener asks for it")
	}
	if err := tt.Click("Ask a follow-up"); err != nil {
		t.Fatal(err)
	}
	if !a.explanation.asking {
		t.Fatal("the follow-up action didn't open the composer")
	}
	if err := tt.Click("Ask about this song…"); err != nil {
		t.Fatal(err)
	}
	tt.Type("  Why does it sound cinematic?  ")
	if a.explanation.draft != "  Why does it sound cinematic?  " {
		t.Fatalf("typed draft %q, loading %v", a.explanation.draft, a.explanation.loading)
	}
	tt.Key(0, ui.KeyEnter)
	if len(asked) == 0 {
		t.Fatalf("Enter left draft %q, loading %v, lib %+v", a.explanation.draft, a.explanation.loading, a.explanation.lib)
	}
	req = wait(t, asked, "the follow-up")
	if req.Question != "Why does it sound cinematic?" || req.Track.ID != "7" {
		t.Fatalf("follow-up %+v", req)
	}
	until(t, ch, func() bool { return !a.explanation.loading })
	if a.explanation.draft != "" || a.explanation.answer.Summary != req.Question {
		t.Errorf("draft %q, answer %+v", a.explanation.draft, a.explanation.answer)
	}
	if len(a.explanation.history) != 1 || a.explanation.history[0].Answer.Summary != "Overview of Umbra" {
		t.Errorf("history %+v", a.explanation.history)
	}
	tt.Frame()
	if err := tt.Click("Ask a follow-up"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Ask about this song…"); err != nil {
		t.Fatal(err)
	}
	tt.Type("Keep this draft")
	tt.Key(0, ui.KeyEscape)
	if a.explanation.asking || !a.nowPlaying || a.explanation.draft != "Keep this draft" {
		t.Errorf("Escape: asking %v, now playing %v, draft %q", a.explanation.asking, a.nowPlaying, a.explanation.draft)
	}
}

// A lyric's context menu explains that line and where it is sung, leaving its
// ordinary click to seek.
func TestExplainLyricFromContextMenu(t *testing.T) {
	a := newTestApp()
	posts(a)
	asked := make(chan music.ExplanationRequest, 1)
	fake(a).explain = func(req music.ExplanationRequest) (*music.Explanation, error) {
		asked <- req
		return &music.Explanation{}, nil
	}
	a.pl.Cue([]music.Track{song("2", "宝船")})
	a.pl.SetLyrics([]lyrics.Line{{At: 12 * time.Second, Text: "第一句"}, {At: 18 * time.Second, Text: "第二句"}})
	a.nowPlaying, a.npPane = true, paneLyrics
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Advance(time.Second)
	tt.Frame()
	if err := tt.RightClick("第二句"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Explain This Lyric"); err != nil {
		t.Fatalf("%v; the menu shows %q", err, tt.Menu())
	}
	req := wait(t, asked, "the lyric explanation")
	if req.Lyric != "第二句" || req.Position != 18000 || req.Track.ID != "2" {
		t.Errorf("asked with %+v", req)
	}
	if a.npPane != paneExplain || a.explanation.lyric != "第二句" {
		t.Errorf("pane %d, lyric %q", a.npPane, a.explanation.lyric)
	}
}

// Another enabled plugin explains a track whose own source doesn't, as an AI
// provider can for local files.
func TestExplanationUsesAnotherPlugin(t *testing.T) {
	a := newTestApp()
	without(a, "explain")
	provider := newFakeSource("explainer")
	for name := range provider.caps {
		delete(provider.caps, name)
	}
	provider.caps["explain"] = true
	addFake(a, provider)
	a.pl.Cue([]music.Track{song("1", "Local Song")})
	if lib := a.explanationLib(a.pl.Current()); lib == nil || lib.id != "explainer" {
		t.Fatalf("provider is %+v", lib)
	}
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if _, ok := tt.Find("Plugin"); ok {
		t.Error("a provider-only plugin appeared as a music source")
	}
}

// A streaming provider's partial answer shows while the request is still
// running, and its final answer replaces the partial one.
func TestExplanationShowsPartialAnswer(t *testing.T) {
	a := newTestApp()
	ch := posts(a)
	started := make(chan struct{}, 1)
	finish := make(chan struct{})
	fake(a).explainStream = func(req music.ExplanationRequest, publish func(music.Explanation)) (*music.Explanation, error) {
		publish(music.Explanation{Title: "Umbra", Summary: "The opening is still arriving."})
		started <- struct{}{}
		<-finish
		return &music.Explanation{Title: "Umbra", Summary: "The complete answer."}, nil
	}
	a.pl.Cue([]music.Track{song("7", "Umbra")})
	a.pl.SetLyrics(nil)
	a.nowPlaying = true
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.Click("Explain"); err != nil {
		t.Fatal(err)
	}
	wait(t, started, "the stream")
	until(t, ch, func() bool { return a.explanation.answer != nil })
	if !a.explanation.loading || a.explanation.answer.Summary != "The opening is still arriving." {
		t.Fatalf("partial answer %+v, loading %v", a.explanation.answer, a.explanation.loading)
	}
	close(finish)
	until(t, ch, func() bool { return !a.explanation.loading })
	if a.explanation.answer.Summary != "The complete answer." {
		t.Errorf("final answer %+v", a.explanation.answer)
	}
}

// Leaving a song for another and returning restores its completed explanation
// from its session rather than asking the provider again.
func TestExplanationRestoresTrackSession(t *testing.T) {
	a := newTestApp()
	ch := posts(a)
	asked := make(chan string, 4)
	fake(a).explain = func(req music.ExplanationRequest) (*music.Explanation, error) {
		asked <- req.Track.ID
		return &music.Explanation{Title: req.Track.Name, Summary: "Notes for " + req.Track.Name}, nil
	}
	a.pl.Cue([]music.Track{song("1", "First"), song("2", "Second")})
	a.pl.SetLyrics(nil)
	a.nowPlaying = true
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.Click("Explain"); err != nil {
		t.Fatal(err)
	}
	if id := wait(t, asked, "the first explanation"); id != "1" {
		t.Fatalf("first song %s", id)
	}
	until(t, ch, func() bool { return !a.explanation.loading })
	a.pl.Next()
	tt.Frame()
	if id := wait(t, asked, "the second explanation"); id != "2" {
		t.Fatalf("second song %s", id)
	}
	until(t, ch, func() bool { return !a.explanation.loading })
	a.pl.Jump(0)
	tt.Frame()
	if len(asked) != 0 {
		t.Error("asked for the first song again")
	}
	if a.explanation.answer == nil || a.explanation.answer.Summary != "Notes for First" || !a.explanation.loaded {
		t.Errorf("restored %+v", a.explanation.answer)
	}
}

// Saved sessions survive a disk round trip with their history and current
// answer intact.
func TestExplanationSessionStore(t *testing.T) {
	path := t.TempDir() + "/explanations.json"
	sessions := map[string]explanationSession{
		"fake\x001\x00explainer": {
			History: []explanationEntry{{Question: "Why?", Answer: music.Explanation{Summary: "Because"}}},
			Answer:  &music.Explanation{Title: "Umbra"}, Complete: true, SavedAt: 1,
		},
	}
	if err := saveExplanationSessions(path, sessions); err != nil {
		t.Fatal(err)
	}
	got := loadExplanationSessions(path)
	saved := got["fake\x001\x00explainer"]
	if !saved.Complete || saved.Answer.Title != "Umbra" || len(saved.History) != 1 || saved.History[0].Question != "Why?" {
		t.Errorf("loaded %+v", saved)
	}
}
