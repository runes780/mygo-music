package app

import (
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"mygo-music/internal/music"
)

// explanationState is what the Explain pane has asked and received for the
// track playing. Requests are numbered so a slow answer can't replace a newer
// song's or question's.
type explanationState struct {
	track    string
	lib      *library
	history  []explanationEntry
	answer   *music.Explanation
	loading  bool
	loaded   bool
	err      string
	lyric    string
	position int64
	question string
	draft    string
	asking   bool
	askInput ui.Handle
	request  uint64
	scroll   ui.ScrollState
}

// explanationLib prefers the track's own source, but lets any enabled plugin
// explain it. That's what makes an explanation provider work for local files.
func (a *app) explanationLib(tr *music.Track) *library {
	if tr != nil {
		if lib := a.trackLib(tr); lib.can("explain") {
			return lib
		}
	}
	for _, lib := range a.libs {
		if lib.can("explain") {
			return lib
		}
	}
	return nil
}

// explanationFor makes the pane's state that of tr, restoring its saved notes.
// A partial answer restored after leaving its stream is loaded anew.
func (a *app) explanationFor(tr *music.Track) *explanationState {
	es := &a.explanation
	key := tr.Source + "\x00" + tr.ID
	lib := a.explanationLib(tr)
	if es.track != key || es.lib != lib {
		a.keepExplanationSession(es, false)
		*es = explanationState{track: key, lib: lib}
		if lib != nil {
			if saved, ok := a.explanationSessions[key+"\x00"+lib.id]; ok {
				es.history = append([]explanationEntry(nil), saved.History...)
				es.answer, es.loaded = saved.Answer, saved.Complete
				es.lyric, es.position, es.question = saved.Lyric, saved.Position, saved.Question
			}
		}
	}
	return es
}

// archiveAnswer keeps the current exchange before another lyric or follow-up
// takes its place.
func (es *explanationState) archiveAnswer() {
	if es.answer == nil || !es.loaded {
		return
	}
	es.history = append(es.history, explanationEntry{Lyric: es.lyric, Position: es.position, Question: es.question, Answer: *es.answer})
	es.answer, es.loaded = nil, false
}

// explainLyric opens the pane on a line the listener chose, at its time in the
// song. Choosing another line adds to the song's notes rather than erasing them.
func (a *app) explainLyric(tr *music.Track, text string, at time.Duration) {
	es := a.explanationFor(tr)
	es.archiveAnswer()
	es.lyric, es.position = text, at.Milliseconds()
	es.question = ""
	es.scroll = ui.ScrollState{}
	a.keepExplanationSession(es, true)
	a.nowPlaying = true
	a.choosePane(paneExplain)
}

// loadExplanation asks the provider for the pane's current song, lyric or
// follow-up. It runs off the UI thread and only its latest answer lands.
func (a *app) loadExplanation(tr *music.Track) {
	es := a.explanationFor(tr)
	if es.loading || es.lib == nil {
		return
	}
	explainer, ok := es.lib.src.(music.Explainer)
	if !ok {
		return
	}
	es.loading, es.err = true, ""
	es.request++
	request, key, lib := es.request, es.track, es.lib
	req := music.ExplanationRequest{Track: *tr, Lyric: es.lyric, Position: es.position, Question: es.question}
	go func() {
		var answer *music.Explanation
		var err error
		if streamer, ok := explainer.(music.StreamingExplainer); ok && lib.can("explainStream") {
			answer, err = streamer.ExplainStream(req, func(partial music.Explanation) {
				a.post(func() {
					if es.track == key && es.request == request {
						es.answer = &partial
					}
				})
			})
		} else {
			answer, err = explainer.Explain(req)
		}
		answer, err = answered(answer, err)
		a.post(func() {
			if es.track != key || es.request != request {
				return
			}
			es.loading = false
			if err != nil {
				es.err = a.problem(lib, err, T("Couldn't explain this song"))
				return
			}
			es.answer, es.loaded, es.draft = answer, true, ""
			a.keepExplanationSession(es, true)
		})
	}()
}

// askExplanation sends the follow-up being written, keeping the draft if it
// fails so the listener can send it again.
func (a *app) askExplanation(tr *music.Track) {
	es := a.explanationFor(tr)
	question := strings.TrimSpace(es.draft)
	if question == "" || es.loading {
		return
	}
	es.archiveAnswer()
	es.question, es.lyric, es.position = question, "", 0
	es.asking = false
	a.keepExplanationSession(es, true)
	a.loadExplanation(tr)
}

func (a *app) explanationPane(c *ui.Context, tr *music.Track) {
	es := a.explanationFor(tr)
	if !es.loaded && !es.loading && es.err == "" {
		a.loadExplanation(tr)
	}

	scroll := ui.Scroll(c.Key("explanation")).Grow(1).TrackScroll(&es.scroll).Padding(4, 12, 36, 0)
	scroll.Children(func() {
		switch {
		case es.loading && es.answer == nil && len(es.history) == 0:
			ui.Column(c).Grow(1).Center().Gap(14).Children(func() {
				ui.Icon(c, iconExplain).Size(38, 38).TextColor(white.Alpha(0.45))
				ui.Text(c, T("Researching this song…")).FontSize(16).FontWeight(600).TextColor(white.Alpha(0.6))
			})
		case es.err != "" && es.answer == nil && len(es.history) == 0:
			ui.Column(c).Grow(1).Center().Gap(14).Children(func() {
				ui.Text(c, es.err).FontSize(15).TextColor(white.Alpha(0.6))
				if pillButton(c.Key("retry"), iconRefresh, T("Retry"), white.Alpha(0.14), white).Clicked() {
					a.loadExplanation(tr)
				}
			})
		case (es.answer == nil || explanationEmpty(es.answer)) && len(es.history) == 0:
			ui.Column(c).Grow(1).Center().Gap(12).Children(func() {
				ui.Icon(c, iconExplain).Size(36, 36).TextColor(white.Alpha(0.3))
				ui.Text(c, T("No explanation for this song yet")).FontSize(16).FontWeight(600).TextColor(white.Alpha(0.5))
			})
		default:
			a.explanationArticle(c, tr, es)
		}
	})
}

func explanationEmpty(e *music.Explanation) bool {
	return e.Title == "" && e.Summary == "" && len(e.Genres) == 0 && len(e.Sections) == 0 && len(e.Sources) == 0
}

// explanationFollowUp keeps conversation out of the reading view until the
// listener asks for it. The field replaces its action in the same place.
func (a *app) explanationFollowUp(c *ui.Context, tr *music.Track, es *explanationState) {
	if es.loading {
		return
	}
	if es.asking {
		a.explanationComposer(c, tr, es)
		return
	}
	b := ui.ButtonBase(c.Key("ask-follow-up")).Padding(7, 0).Gap(7).AlignItems(ui.Center).AlignSelf(ui.Start).
		FocusRing(false).Label(T("Ask a follow-up"))
	fg := white.Alpha(0.46)
	if b.Hovered() {
		fg = white.Alpha(0.85)
	}
	b.TextColor(b.AnimateColor("fg", fg, ui.Animation{Duration: 120 * time.Millisecond}))
	b.Children(func() {
		ui.Icon(c, iconPlus).Size(12, 12)
		ui.Text(c, T("Ask a follow-up")).FontSize(12.5).FontWeight(600)
	})
	if b.Clicked() {
		es.asking = true
		es.askInput.Focus(c)
	}
}

var explanationReveal = ui.Animation{Spring: ui.SpringOptions{Duration: 260 * time.Millisecond, Bounce: 0}}

func (a *app) explanationComposer(c *ui.Context, tr *music.Track, es *explanationState) {
	row := ui.Row(c.Key("ask")).Height(46).Padding(6, 6, 6, 14).Gap(7).AlignItems(ui.Center).MaxWidth(470).AlignSelf(ui.Start).
		Radius(13).Background(white.Alpha(0.05)).Border(0.5, white.Alpha(0.1)).
		Transition(ui.ElementTransition{Animation: &explanationReveal, Height: true, Enter: &ui.Motion{Y: 4}, Exit: &ui.Motion{Y: 4}})
	row.Children(func() {
		in := ui.TextInputBase(c.Key("question"), &es.draft).Bind(&es.askInput).
			Placeholder(T("Ask about this song…")).Label(T("Ask about this song…")).
			FontSize(14).TextColor(white.Alpha(0.9)).Grow(1).Shrink(1).MinWidth(0)
		in.OnShortcut(0, ui.KeyEscape, func() { es.asking = false })
		disabled := strings.TrimSpace(es.draft) == ""
		send := ui.ButtonBase(c.Key("send")).Size(32, 32).Center().Radius(999).Label(T("Ask")).Tooltip(T("Ask")).Disabled(disabled)
		bg, fg := white.Alpha(0.82), ui.Hex("#151519")
		if disabled {
			bg, fg = white.Alpha(0.06), white.Alpha(0.25)
		} else if send.Hovered() {
			bg = white
		}
		send.Background(send.AnimateColor("bg", bg, ui.Animation{Duration: 120 * time.Millisecond})).TextColor(fg)
		send.Children(func() { ui.Icon(c, iconArrowUp).Size(16, 16) })
		if iconButton(c.Key("cancel-ask"), iconX, 13, white.Alpha(0.42), T("Cancel")).Clicked() {
			es.asking = false
		}
		if send.Clicked() || in.Submitted() {
			a.askExplanation(tr)
		}
	})
}

// explanationArticle is the provider's answer as a native, scrollable article,
// with its interpretation separate from the links that back it.
func (a *app) explanationArticle(c *ui.Context, tr *music.Track, es *explanationState) {
	ui.Column(c).Gap(20).MaxWidth(560).AlignSelf(ui.Start).Children(func() {
		for i, entry := range es.history {
			ui.Column(c.Key(i)).Gap(14).Children(func() {
				label := entry.Question
				if entry.Lyric != "" {
					label = Tf("Lyric at %s", clock(time.Duration(entry.Position)*time.Millisecond)) + " · “" + entry.Lyric + "”"
				} else if label == "" {
					label = T("Overview")
				}
				ui.Text(c, label).FontSize(13).FontWeight(650).LineHeight(1.45).TextColor(white.Alpha(0.48))
				a.explanationDetails(c, &entry.Answer, i)
			})
			ui.Box(c).Height(0.5).Background(white.Alpha(0.08)).Margin(4, 0, 2, 0)
		}

		if e := es.answer; e != nil {
			ui.Row(c).AlignItems(ui.Start).Gap(12).Children(func() {
				ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(6).Children(func() {
					title := e.Title
					if title == "" {
						title = tr.Name
					}
					ui.Text(c, title).FontSize(26).FontWeight(750).LetterSpacing(-0.5).LineHeight(1.16)
					subtitle := e.Subtitle
					if subtitle == "" {
						subtitle = tr.ArtistNames()
					}
					if subtitle != "" {
						ui.Text(c, subtitle).FontSize(13.5).FontWeight(600).TextColor(white.Alpha(0.55))
					}
				})
				refresh := iconButton(c.Key("refresh"), iconRefresh, 15, white.Alpha(0.5), T("Refresh"))
				if es.loading {
					refresh.Disabled(true).Opacity(0.4)
				}
				if refresh.Clicked() {
					a.loadExplanation(tr)
				}
			})
		}

		if es.lyric != "" {
			ui.Column(c).Padding(13, 15).Gap(6).Radius(12).Background(white.Alpha(0.055)).AlignItems(ui.Start).Children(func() {
				ui.Text(c, Tf("Lyric at %s", clock(time.Duration(es.position)*time.Millisecond))).FontSize(11).FontWeight(700).TextColor(white.Alpha(0.42))
				ui.Text(c, "“"+es.lyric+"”").FontSize(16).FontWeight(650).LineHeight(1.4).TextColor(white.Alpha(0.88))
			})
		} else if es.question != "" {
			ui.Text(c, "“"+es.question+"”").FontSize(15.5).FontWeight(650).LineHeight(1.45).TextColor(white.Alpha(0.82))
		}
		if es.answer != nil {
			a.explanationDetails(c, es.answer, -1)
		}
		if es.err != "" {
			ui.Text(c, es.err).FontSize(12.5).LineHeight(1.45).TextColor(white.Alpha(0.55))
		}
		if es.loading {
			ui.Text(c, T("Writing…")).FontSize(11.5).FontWeight(600).TextColor(white.Alpha(0.45)).Margin(2, 0, 0, 0)
		} else {
			ui.Text(c, Tf("Explained by %s", es.lib.name())).FontSize(11.5).TextColor(white.Alpha(0.32)).Margin(2, 0, 0, 0)
		}
		a.explanationFollowUp(c, tr, es)
	})
}

// explanationDetails renders an answer's musical content without its title, so
// earlier exchanges and the current one share the same hierarchy.
func (a *app) explanationDetails(c *ui.Context, e *music.Explanation, owner int) {
	if len(e.Genres) > 0 {
		ui.Text(c, strings.Join(e.Genres, "  ·  ")).FontSize(12).FontWeight(600).LineHeight(1.5).TextColor(white.Alpha(0.52))
	}
	if e.Summary != "" {
		ui.Text(c, e.Summary).FontSize(15.5).LineHeight(1.6).TextColor(white.Alpha(0.88))
	}
	for i, section := range e.Sections {
		ui.Column(c.Key(struct{ owner, section int }{owner, i})).Gap(9).AlignItems(ui.Start).Children(func() {
			if section.Title != "" {
				ui.Text(c, section.Title).FontSize(18).FontWeight(700).LetterSpacing(-0.15)
			}
			if section.Body != "" {
				ui.Text(c, section.Body).FontSize(14.5).LineHeight(1.6).TextColor(white.Alpha(0.74))
			}
			for _, item := range section.Items {
				ui.Row(c).Gap(10).AlignItems(ui.Start).Children(func() {
					ui.Text(c, "•").FontSize(14.5).TextColor(white.Alpha(0.4))
					ui.Text(c, item).FontSize(14.5).LineHeight(1.55).TextColor(white.Alpha(0.74)).Grow(1).Shrink(1).MinWidth(0)
				})
			}
		})
	}
	if len(e.Sources) > 0 {
		ui.Column(c).Gap(7).AlignItems(ui.Start).Children(func() {
			ui.Text(c, T("Sources")).FontSize(12.5).FontWeight(700).TextColor(white.Alpha(0.5))
			for i, source := range e.Sources {
				a.explanationSource(c.Key(i), source)
			}
		})
	}
}

var openExternal = mygo.Shell.OpenExternal

func (a *app) explanationSource(c *ui.Context, source music.ExplanationSource) {
	b := ui.ButtonBase(c).Padding(9, 12).Gap(10).Radius(10).AlignItems(ui.Center).FocusRing(false).Label(source.Title)
	if b.Hovered() {
		b.Background(white.Alpha(0.07))
	}
	b.Children(func() {
		ui.Icon(c, iconExplain).Size(14, 14).TextColor(white.Alpha(0.45))
		ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, source.Title).FontSize(13).FontWeight(600).TextColor(white.Alpha(0.75)).SingleLine()
			if source.Publisher != "" {
				ui.Text(c, source.Publisher).FontSize(11.5).TextColor(white.Alpha(0.4)).SingleLine()
			}
		})
	})
	if b.Clicked() && source.URL != "" {
		if err := openExternal(source.URL); err != nil {
			a.toast = T("Couldn't open this source")
		}
	}
}
