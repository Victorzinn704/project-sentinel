package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/seu-usuario/project-sentinel/internal/domain"
	sentinelcrypto "github.com/seu-usuario/project-sentinel/internal/infrastructure/crypto"
)

var ErrInvalidAccountID = errors.New("invalid account id")

type SessionStore interface {
	Load(accountID string) (*domain.Session, error)
	Save(session *domain.Session) error
	Delete(accountID string) error
}

type FileSessionStore struct {
	root      string
	encryptor sentinelcrypto.Encryptor
}

func NewFileSessionStore(root string, encryptor sentinelcrypto.Encryptor) *FileSessionStore {
	return &FileSessionStore{
		root:      root,
		encryptor: encryptor,
	}
}

func (s *FileSessionStore) Load(accountID string) (*domain.Session, error) {
	path, err := s.sessionPath(accountID)
	if err != nil {
		return nil, err
	}

	payload, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read session file: %w", err)
	}

	plaintext, err := s.encryptor.Decrypt(payload)
	if err != nil {
		return nil, fmt.Errorf("decrypt session file: %w", err)
	}

	var session domain.Session
	if err := json.Unmarshal(plaintext, &session); err != nil {
		return nil, fmt.Errorf("decode session JSON: %w", err)
	}

	return &session, nil
}

func (s *FileSessionStore) Save(session *domain.Session) error {
	path, err := s.sessionPath(session.AccountID)
	if err != nil {
		return err
	}

	plaintext, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("encode session JSON: %w", err)
	}

	payload, err := s.encryptor.Encrypt(plaintext)
	if err != nil {
		return fmt.Errorf("encrypt session file: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create session directory: %w", err)
	}

	tmp := path + ".tmp"
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create temporary session file: %w", err)
	}

	written, err := file.Write(payload)
	if err != nil {
		closeErr := file.Close()
		if closeErr != nil {
			return withTempCleanup(fmt.Errorf("write encrypted session file: %w; close failed: %v", err, closeErr), tmp)
		}
		return withTempCleanup(fmt.Errorf("write encrypted session file: %w", err), tmp)
	}
	if written != len(payload) {
		closeErr := file.Close()
		if closeErr != nil {
			return withTempCleanup(fmt.Errorf("write encrypted session file: partial write; close failed: %v", closeErr), tmp)
		}
		return withTempCleanup(fmt.Errorf("write encrypted session file: partial write"), tmp)
	}

	if err := file.Sync(); err != nil {
		closeErr := file.Close()
		if closeErr != nil {
			return withTempCleanup(fmt.Errorf("sync encrypted session file: %w; close failed: %v", err, closeErr), tmp)
		}
		return withTempCleanup(fmt.Errorf("sync encrypted session file: %w", err), tmp)
	}

	if err := file.Close(); err != nil {
		return withTempCleanup(fmt.Errorf("close encrypted session file: %w", err), tmp)
	}

	if err := os.Rename(tmp, path); err != nil {
		return withTempCleanup(fmt.Errorf("commit session file: %w", err), tmp)
	}

	return nil
}

func (s *FileSessionStore) Delete(accountID string) error {
	path, err := s.sessionPath(accountID)
	if err != nil {
		return err
	}

	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete session file: %w", err)
	}

	return nil
}

func (s *FileSessionStore) Ready() error {
	info, err := os.Stat(s.root)
	if err != nil {
		return fmt.Errorf("session store is not accessible: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("session store path is not a directory")
	}

	return nil
}

func (s *FileSessionStore) sessionPath(accountID string) (string, error) {
	if !safeAccountID(accountID) {
		return "", ErrInvalidAccountID
	}

	return filepath.Join(s.root, accountID+".json.enc"), nil
}

func safeAccountID(accountID string) bool {
	if accountID == "" {
		return false
	}
	if strings.Contains(accountID, "..") {
		return false
	}
	if strings.ContainsAny(accountID, `/\`) {
		return false
	}

	return true
}

func withTempCleanup(primary error, tmp string) error {
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w; cleanup temporary session file failed: %v", primary, err)
	}

	return primary
}
