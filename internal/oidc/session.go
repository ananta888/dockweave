package oidc

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const defaultCookieName = "dockweave_oidc"

// SessionStore is the session repository port.
type SessionStore interface {
	Save(w http.ResponseWriter, s Session) error
	Get(r *http.Request) (*Session, error)
	Delete(w http.ResponseWriter)
}

// CookieStore is an encrypted-cookie adapter for SessionStore. The session is
// sealed with AES-GCM so a client cannot forge or read it.
type CookieStore struct {
	Name   string
	Key    [32]byte
	Secure bool
}

func (c CookieStore) cookieName() string {
	if c.Name == "" {
		return defaultCookieName
	}
	return c.Name
}

func (c CookieStore) seal(s Session) (string, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(c.Key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nonce, nonce, raw, nil)
	return base64.RawURLEncoding.EncodeToString(ct), nil
}

func (c CookieStore) open(value string) (*Session, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, ErrInvalidSession
	}
	block, err := aes.NewCipher(c.Key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(raw) < gcm.NonceSize() {
		return nil, ErrInvalidSession
	}
	pt, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return nil, ErrInvalidSession
	}
	var s Session
	if err := json.Unmarshal(pt, &s); err != nil {
		return nil, ErrInvalidSession
	}
	if s.Expired(time.Now()) {
		return nil, ErrNoSession
	}
	return &s, nil
}

// Save writes an encrypted, HttpOnly session cookie.
func (c CookieStore) Save(w http.ResponseWriter, s Session) error {
	value, err := c.seal(s)
	if err != nil {
		return fmt.Errorf("seal session: %w", err)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     c.cookieName(),
		Value:    value,
		Path:     "/",
		Expires:  s.ExpiresAt,
		HttpOnly: true,
		Secure:   c.Secure,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// Get decrypts and validates the session cookie.
func (c CookieStore) Get(r *http.Request) (*Session, error) {
	cookie, err := r.Cookie(c.cookieName())
	if err != nil {
		return nil, ErrNoSession
	}
	return c.open(cookie.Value)
}

// Delete clears the session cookie.
func (c CookieStore) Delete(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: c.cookieName(), Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}
