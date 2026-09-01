// Package auth provides the single-admin authentication the appliance needs.
//
// Phase 1 left the API reachable by anyone on the LAN; this closes that. The
// model is deliberately small: one admin account, server-side sessions, no
// registration flow. A home appliance with one owner does not need roles, and
// every extra concept here is another way to get authorisation wrong.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrNoAccount      = errors.New("no admin account has been created yet")
	ErrBadCredentials = errors.New("incorrect username or password")
	ErrWeakPassword   = errors.New("password must be at least 10 characters")
	ErrAlreadySetup   = errors.New("an admin account already exists")
)

type account struct {
	Username  string    `json:"username"`
	Hash      string    `json:"hash"`
	CreatedAt time.Time `json:"created_at"`
}

type session struct {
	token     string
	username  string
	expiresAt time.Time
}

type Service struct {
	path string
	ttl  time.Duration

	mu       sync.RWMutex
	acct     *account
	sessions map[string]*session
}

func New(secretsDir string, ttl time.Duration) (*Service, error) {
	if ttl <= 0 {
		ttl = 168 * time.Hour
	}
	s := &Service{
		path:     filepath.Join(secretsDir, "admin.json"),
		ttl:      ttl,
		sessions: map[string]*session{},
	}
	if err := s.load(); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return s, nil
}

func (s *Service) load() error {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var a account
	if err := json.Unmarshal(raw, &a); err != nil {
		return fmt.Errorf("parse admin account: %w", err)
	}
	s.mu.Lock()
	s.acct = &a
	s.mu.Unlock()
	return nil
}

// NeedsSetup reports whether the first-run wizard should be shown.
func (s *Service) NeedsSetup() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.acct == nil
}

// Setup creates the one admin account. It refuses if an account already exists,
// so an unauthenticated caller cannot reset the box by replaying the first-run
// wizard.
func (s *Service) Setup(username, password string) error {
	if !s.NeedsSetup() {
		return ErrAlreadySetup
	}
	if len(username) < 3 {
		return errors.New("username must be at least 3 characters")
	}
	if len(password) < 10 {
		return ErrWeakPassword
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	a := account{Username: username, Hash: string(hash), CreatedAt: time.Now()}

	raw, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(s.path, raw, 0o600); err != nil {
		return fmt.Errorf("write admin account: %w", err)
	}

	s.mu.Lock()
	s.acct = &a
	s.mu.Unlock()
	return nil
}

// Login verifies credentials and issues a session token.
func (s *Service) Login(username, password string) (string, time.Time, error) {
	s.mu.RLock()
	a := s.acct
	s.mu.RUnlock()

	if a == nil {
		return "", time.Time{}, ErrNoAccount
	}
	// Compare the hash even when the username is wrong, so a wrong username and
	// a wrong password take the same time and cannot be told apart by timing.
	userOK := subtle.ConstantTimeCompare([]byte(username), []byte(a.Username)) == 1
	passOK := bcrypt.CompareHashAndPassword([]byte(a.Hash), []byte(password)) == nil
	if !userOK || !passOK {
		return "", time.Time{}, ErrBadCredentials
	}

	token, err := newToken()
	if err != nil {
		return "", time.Time{}, err
	}
	exp := time.Now().Add(s.ttl)

	s.mu.Lock()
	s.sessions[token] = &session{token: token, username: username, expiresAt: exp}
	s.mu.Unlock()
	return token, exp, nil
}

// Validate returns the username behind a session token.
func (s *Service) Validate(token string) (string, bool) {
	if token == "" {
		return "", false
	}
	s.mu.RLock()
	sess, ok := s.sessions[token]
	s.mu.RUnlock()
	if !ok {
		return "", false
	}
	if time.Now().After(sess.expiresAt) {
		s.Logout(token)
		return "", false
	}
	return sess.username, true
}

func (s *Service) Logout(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

// ChangePassword requires the current one, so a stolen session cannot be
// upgraded into permanent account control.
func (s *Service) ChangePassword(username, current, next string) error {
	if _, _, err := s.Login(username, current); err != nil {
		return err
	}
	if len(next) < 10 {
		return ErrWeakPassword
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(next), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.acct.Hash = string(hash)
	a := *s.acct
	// Every existing session is invalidated: changing a password is what you
	// do when you think someone else has access.
	s.sessions = map[string]*session{}
	s.mu.Unlock()

	raw, _ := json.MarshalIndent(a, "", "  ")
	return os.WriteFile(s.path, raw, 0o600)
}

// PurgeExpired drops timed-out sessions. Called periodically so the map does
// not grow without bound on a long-running daemon.
func (s *Service) PurgeExpired() int {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for k, v := range s.sessions {
		if now.After(v.expiresAt) {
			delete(s.sessions, k)
			n++
		}
	}
	return n
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
