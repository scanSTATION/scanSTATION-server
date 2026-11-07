package api

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// Session represents a multi-page scanning session.
type Session struct {
	ID         string
	CreatedAt  time.Time
	Params     ScanParams
	PagePDFs   []string // paths to individual page PDFs (temporary location)
	PageExtras []string // companion files of the pages (e.g. JSON metadata)
	Backend    string   // backend that scanned the pages
	Status     SessionStatus
	Error      string
	FinalFile  string // path to the combined PDF after finish
	mu         sync.Mutex
}

// Lock acquires the session mutex.
func (s *Session) Lock() { s.mu.Lock() }

// Unlock releases the session mutex.
func (s *Session) Unlock() { s.mu.Unlock() }

// SessionStore manages multi-page scan sessions in memory.
type SessionStore struct {
	sessions map[string]*Session
	mu       sync.RWMutex
}

// NewSessionStore creates an empty session store.
func NewSessionStore() *SessionStore {
	return &SessionStore{
		sessions: make(map[string]*Session),
	}
}

// Create creates a new session with the given parameters and returns it.
func (store *SessionStore) Create(params ScanParams) *Session {
	id := generateSessionID()
	sess := &Session{
		ID:        id,
		CreatedAt: time.Now(),
		Params:    params,
		Status:    StatusScanning,
	}
	store.mu.Lock()
	store.sessions[id] = sess
	store.mu.Unlock()
	return sess
}

// Get retrieves a session by ID. Returns nil and false if not found.
func (store *SessionStore) Get(id string) (*Session, bool) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	sess, ok := store.sessions[id]
	return sess, ok
}

// Delete removes a session from the store.
func (store *SessionStore) Delete(id string) {
	store.mu.Lock()
	delete(store.sessions, id)
	store.mu.Unlock()
}

// generateSessionID produces a random 16-character hex string.
func generateSessionID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
