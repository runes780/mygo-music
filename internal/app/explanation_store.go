package app

import (
	"cmp"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"slices"
	"time"

	"mygo-music/internal/music"
)

// explanationEntry is one finished exchange in a song's listening notes.
type explanationEntry struct {
	Lyric    string            `json:"lyric,omitempty"`
	Position int64             `json:"position,omitempty"`
	Question string            `json:"question,omitempty"`
	Answer   music.Explanation `json:"answer"`
}

// explanationSession is the durable part of Explain's state. Input, focus and
// loading state deliberately stay out of it.
type explanationSession struct {
	History  []explanationEntry `json:"history,omitempty"`
	Answer   *music.Explanation `json:"answer,omitempty"`
	Lyric    string             `json:"lyric,omitempty"`
	Position int64              `json:"position,omitempty"`
	Question string             `json:"question,omitempty"`
	Complete bool               `json:"complete,omitempty"`
	SavedAt  int64              `json:"savedAt"`
}

const explanationSessionLimit = 24

// loadExplanationSessions reads saved listening notes. A broken file doesn't
// keep the player from starting.
func loadExplanationSessions(path string) map[string]explanationSession {
	var sessions map[string]explanationSession
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err == nil {
		err = json.Unmarshal(b, &sessions)
	}
	if err != nil {
		log.Printf("explanations: reading saved sessions: %v", err)
	}
	return sessions
}

// saveExplanationSessions writes the newest sessions whole, so interruption
// leaves the previous file intact.
func saveExplanationSessions(path string, sessions map[string]explanationSession) error {
	if len(sessions) > explanationSessionLimit {
		keys := make([]string, 0, len(sessions))
		for key := range sessions {
			keys = append(keys, key)
		}
		slices.SortFunc(keys, func(x, y string) int {
			return cmp.Compare(sessions[x].SavedAt, sessions[y].SavedAt)
		})
		for _, key := range keys[:len(keys)-explanationSessionLimit] {
			delete(sessions, key)
		}
	}
	b, err := json.Marshal(sessions)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path+".part", b, 0o600); err != nil {
		return err
	}
	return os.Rename(path+".part", path)
}

func (a *app) keepExplanationSession(es *explanationState, persist bool) {
	if es.track == "" || es.lib == nil {
		return
	}
	if a.explanationSessions == nil {
		a.explanationSessions = map[string]explanationSession{}
	}
	session := explanationSession{
		History: slices.Clone(es.history), Answer: es.answer, Lyric: es.lyric,
		Position: es.position, Question: es.question, Complete: es.loaded, SavedAt: time.Now().UnixMilli(),
	}
	key := es.track + "\x00" + es.lib.id
	a.explanationSessions[key] = session
	if persist {
		if err := saveExplanationSessions(userDataDir("explanations.json"), a.explanationSessions); err != nil {
			log.Printf("explanations: saving sessions: %v", err)
		}
	}
}
