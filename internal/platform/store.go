package platform

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

// One encrypted workspace is committed atomically. The first version deliberately
// uses a single process; reads and mutations never share mutable model objects.
type Store struct {
	mu        sync.RWMutex
	db        *sql.DB
	aead      cipher.AEAD
	workspace Workspace
	lock      *os.File
}

func OpenStore(path string, key []byte) (*Store, bool, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, false, err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, false, err
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lock.Close()
		return nil, false, fmt.Errorf("此数据库已被另一个 API2MCP 进程占用")
	}
	opened := false
	defer func() {
		if !opened {
			_ = unix.Flock(int(lock.Fd()), unix.LOCK_UN)
			lock.Close()
		}
	}()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, false, err
	}
	file.Close()
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, false, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, false, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, false, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, aead: aead, lock: lock}
	fail := func(err error) (*Store, bool, error) { db.Close(); return nil, false, err }
	if _, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA busy_timeout=5000;
		CREATE TABLE IF NOT EXISTS workspace (id INTEGER PRIMARY KEY CHECK (id=1), payload BLOB NOT NULL)`); err != nil {
		return fail(err)
	}
	var blob []byte
	err = db.QueryRow(`SELECT payload FROM workspace WHERE id=1`).Scan(&blob)
	if err == sql.ErrNoRows {
		s.workspace = emptyWorkspace()
		if err := s.write(s.workspace); err != nil {
			return fail(err)
		}
		opened = true
		return s, true, nil
	}
	if err != nil {
		return fail(err)
	}
	if len(blob) < aead.NonceSize() {
		return fail(fmt.Errorf("数据库密文损坏"))
	}
	plain, err := aead.Open(nil, blob[:aead.NonceSize()], blob[aead.NonceSize():], []byte("api2mcp-workspace-v1"))
	if err != nil {
		return fail(fmt.Errorf("无法解密数据库，请确认 ENCRYPTION_KEY 与初次启动一致"))
	}
	if err = json.Unmarshal(plain, &s.workspace); err != nil {
		return fail(err)
	}
	if s.workspace.SchemaVersion != 1 {
		return fail(fmt.Errorf("不支持的数据库版本"))
	}
	opened = true
	return s, false, nil
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.db.Close()
	if s.lock != nil {
		_ = unix.Flock(int(s.lock.Fd()), unix.LOCK_UN)
		_ = s.lock.Close()
		s.lock = nil
	}
	return err
}
func clone(w Workspace) Workspace {
	b, _ := json.Marshal(w)
	var out Workspace
	_ = json.Unmarshal(b, &out)
	return out
}
func (s *Store) Snapshot() Workspace { s.mu.RLock(); defer s.mu.RUnlock(); return clone(s.workspace) }

func (s *Store) Update(fn func(*Workspace) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := clone(s.workspace)
	if err := fn(&next); err != nil {
		return err
	}
	if err := s.write(next); err != nil {
		return err
	}
	s.workspace = next
	return nil
}

func (s *Store) write(w Workspace) error {
	plain, err := json.Marshal(w)
	if err != nil {
		return err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	blob := s.aead.Seal(nonce, nonce, plain, []byte("api2mcp-workspace-v1"))
	_, err = s.db.Exec(`INSERT INTO workspace(id,payload) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload`, blob)
	return err
}
