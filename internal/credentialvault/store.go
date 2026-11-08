package credentialvault

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const (
	envelopeVersion = 1
	// sentinelAAD binds the passphrase-check value to the envelope format.
	sentinelAAD = "dockweave-credential-vault-v1\x00sentinel"
	saltLen     = 16
)

// ErrNotFound is returned when a credential does not exist.
var ErrNotFound = errors.New("credential not found")

// ErrLocked is returned when another process holds the vault lock.
var ErrLocked = errors.New("credential vault is locked by another process")

// Entry is the on-disk metadata plus ciphertext of one credential.
// Ciphertext values are base64; plaintext never touches disk.
type Entry struct {
	Provider   string    `json:"provider"`
	Nonce      string    `json:"nonce"`
	Ciphertext string    `json:"ciphertext"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type sentinel struct {
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

// Envelope is the versioned on-disk format. No plaintext secrets.
type Envelope struct {
	Version     int              `json:"version"`
	KDF         KDFParams        `json:"kdf"`
	Salt        string           `json:"salt"`
	Sentinel    sentinel         `json:"sentinel"`
	Credentials map[string]Entry `json:"credentials"`
}

// Metadata is the public, non-secret view of a credential.
type Metadata struct {
	Name      string    `json:"name"`
	Provider  string    `json:"provider"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Secret is a decrypted credential value. Callers must not log Value.
type Secret struct {
	Name     string
	Provider string
	Value    string
}

// Store is an unlocked, encrypted credential vault.
type Store struct {
	mu       sync.RWMutex
	path     string
	lockFile *os.File
	env      *Envelope
	key      []byte
	closed   bool
}

// Create initializes a new vault at path and returns it unlocked.
// It fails if the file already exists (use Open).
func Create(path, passphrase string) (*Store, error) {
	if len(passphrase) < 8 {
		return nil, errors.New("passphrase must be at least 8 characters")
	}
	s := &Store{path: path, env: &Envelope{
		Version:     envelopeVersion,
		KDF:         DefaultKDFParams(),
		Credentials: map[string]Entry{},
	}}
	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("vault already exists: %s", path)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err := s.lock(); err != nil {
		return nil, err
	}
	if err := s.setPassphraseLocked([]byte(passphrase)); err != nil {
		s.releaseLock()
		return nil, err
	}
	if err := s.writeLocked(); err != nil {
		s.releaseLock()
		return nil, err
	}
	return s, nil
}

// Open unlocks an existing vault. The passphrase is verified against the
// sentinel before any credential is accepted.
func Open(path, passphrase string) (*Store, error) {
	s := &Store{path: path}
	if err := s.lock(); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		s.releaseLock()
		return nil, fmt.Errorf("read vault: %w", err)
	}
	var env Envelope
	if err := json.Unmarshal(b, &env); err != nil {
		s.releaseLock()
		return nil, fmt.Errorf("parse vault: %w", err)
	}
	if env.Version != envelopeVersion {
		s.releaseLock()
		return nil, fmt.Errorf("unsupported vault version %d", env.Version)
	}
	if err := env.KDF.validate(); err != nil {
		s.releaseLock()
		return nil, err
	}
	if env.Credentials == nil {
		env.Credentials = map[string]Entry{}
	}
	s.env = &env
	salt, err := base64.StdEncoding.DecodeString(env.Salt)
	if err != nil || len(salt) < saltLen {
		s.releaseLock()
		return nil, errors.New("invalid vault salt")
	}
	key := deriveKey([]byte(passphrase), salt, env.KDF)
	nonce, err := base64.StdEncoding.DecodeString(env.Sentinel.Nonce)
	if err != nil {
		wipe(key)
		s.releaseLock()
		return nil, errors.New("invalid vault sentinel nonce")
	}
	ct, err := base64.StdEncoding.DecodeString(env.Sentinel.Ciphertext)
	if err != nil {
		wipe(key)
		s.releaseLock()
		return nil, errors.New("invalid vault sentinel")
	}
	check, err := open(ct, nonce, key, []byte(sentinelAAD))
	if err != nil {
		wipe(key)
		s.releaseLock()
		return nil, errors.New("wrong passphrase or corrupted vault")
	}
	if string(check) != "dockweave-vault-ok" {
		wipe(check)
		wipe(key)
		s.releaseLock()
		return nil, errors.New("wrong passphrase or corrupted vault")
	}
	wipe(check)
	s.key = key
	return s, nil
}

func (s *Store) lock() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create vault directory: %w", err)
	}
	f, err := os.OpenFile(s.path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open vault lock: %w", err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return ErrLocked
	}
	s.lockFile = f
	return nil
}

func (s *Store) releaseLock() {
	if s.lockFile != nil {
		_ = unix.Flock(int(s.lockFile.Fd()), unix.LOCK_UN)
		_ = s.lockFile.Close()
		s.lockFile = nil
	}
}

// Close wipes the in-memory key and releases the vault lock.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	wipe(s.key)
	s.key = nil
	s.closed = true
	s.releaseLock()
	return nil
}

// setPassphraseLocked creates a new salt/key and re-encrypts the sentinel.
// Existing credentials must already be re-encrypted by the caller (rotate).
func (s *Store) setPassphraseLocked(pass []byte) error {
	salt, err := randomBytes(saltLen)
	if err != nil {
		return err
	}
	s.env.Salt = base64.StdEncoding.EncodeToString(salt)
	key := deriveKey(pass, salt, s.env.KDF)
	nonce, ct, err := seal([]byte("dockweave-vault-ok"), key, []byte(sentinelAAD))
	if err != nil {
		wipe(key)
		return err
	}
	wipe(s.key)
	s.key = key
	s.env.Sentinel = sentinel{
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ct),
	}
	return nil
}

// Put stores or replaces a credential value. Empty providers are rejected.
func (s *Store) Put(name, provider, value string) error {
	if name == "" {
		return errors.New("credential name must not be empty")
	}
	if provider == "" {
		return errors.New("credential provider must not be empty")
	}
	if value == "" {
		return errors.New("credential value must not be empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.key == nil {
		return errors.New("vault is closed")
	}
	nonce, ct, err := seal([]byte(value), s.key, entryAAD(name))
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	created := now
	if old, ok := s.env.Credentials[name]; ok {
		created = old.CreatedAt
	}
	s.env.Credentials[name] = Entry{
		Provider:   provider,
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ct),
		CreatedAt:  created,
		UpdatedAt:  now,
	}
	return s.writeLocked()
}

// Get decrypts and returns one credential.
func (s *Store) Get(name string) (Secret, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.key == nil {
		return Secret{}, errors.New("vault is closed")
	}
	e, ok := s.env.Credentials[name]
	if !ok {
		return Secret{}, ErrNotFound
	}
	nonce, err := base64.StdEncoding.DecodeString(e.Nonce)
	if err != nil {
		return Secret{}, errors.New("invalid credential nonce")
	}
	ct, err := base64.StdEncoding.DecodeString(e.Ciphertext)
	if err != nil {
		return Secret{}, errors.New("invalid credential ciphertext")
	}
	plain, err := open(ct, nonce, s.key, entryAAD(name))
	if err != nil {
		return Secret{}, err
	}
	return Secret{Name: name, Provider: e.Provider, Value: string(plain)}, nil
}

// Delete removes a credential.
func (s *Store) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.key == nil {
		return errors.New("vault is closed")
	}
	if _, ok := s.env.Credentials[name]; !ok {
		return ErrNotFound
	}
	delete(s.env.Credentials, name)
	return s.writeLocked()
}

// List returns credential metadata sorted by name. Never returns values.
func (s *Store) List() []Metadata {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Metadata, 0, len(s.env.Credentials))
	for name, e := range s.env.Credentials {
		out = append(out, Metadata{Name: name, Provider: e.Provider,
			CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// RotatePassphrase decrypts every credential with the current key and
// re-encrypts it under a fresh salt derived from newPass.
func (s *Store) RotatePassphrase(newPass string) error {
	if len(newPass) < 8 {
		return errors.New("passphrase must be at least 8 characters")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.key == nil {
		return errors.New("vault is closed")
	}
	oldKey := append([]byte(nil), s.key...)
	plain := make(map[string]string, len(s.env.Credentials))
	for name, e := range s.env.Credentials {
		nonce, err := base64.StdEncoding.DecodeString(e.Nonce)
		if err != nil {
			wipe(oldKey)
			return fmt.Errorf("credential %q: invalid nonce", name)
		}
		ct, err := base64.StdEncoding.DecodeString(e.Ciphertext)
		if err != nil {
			wipe(oldKey)
			return fmt.Errorf("credential %q: invalid ciphertext", name)
		}
		v, err := open(ct, nonce, oldKey, entryAAD(name))
		if err != nil {
			wipe(oldKey)
			return fmt.Errorf("credential %q: %w", name, err)
		}
		plain[name] = string(v)
		wipe(v)
	}
	wipe(oldKey)
	if err := s.setPassphraseLocked([]byte(newPass)); err != nil {
		return err
	}
	for name, value := range plain {
		e := s.env.Credentials[name]
		nonce, ct, err := seal([]byte(value), s.key, entryAAD(name))
		if err != nil {
			return err
		}
		e.Nonce = base64.StdEncoding.EncodeToString(nonce)
		e.Ciphertext = base64.StdEncoding.EncodeToString(ct)
		e.UpdatedAt = time.Now().UTC()
		s.env.Credentials[name] = e
	}
	return s.writeLocked()
}

func (s *Store) writeLocked() error {
	b, err := json.MarshalIndent(s.env, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal vault: %w", err)
	}
	return atomicWrite(s.path, b, 0o600)
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace vault: %w", err)
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func entryAAD(name string) []byte {
	h := sha256.Sum256([]byte(name))
	return append([]byte("dockweave-credential-vault-v1\x00entry\x00"), h[:]...)
}
