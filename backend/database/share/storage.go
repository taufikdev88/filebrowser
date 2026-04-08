package share

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gtsteffaniak/filebrowser/backend/common/errors"
	"github.com/gtsteffaniak/filebrowser/backend/common/utils"
	"github.com/gtsteffaniak/filebrowser/backend/database/crud"
	"github.com/gtsteffaniak/filebrowser/backend/database/users"
	"github.com/gtsteffaniak/go-logger/logger"
)

// StorageBackend is the interface to implement for a share storage.
type StorageBackend interface {
	All() ([]*Share, error)
	FindByUserID(userID uint64) ([]*Share, error)
	GetByHash(hash string) (*Share, error)
	GetShareInfoByHash(hash string) (*FrontendShareInfo, error)
	GetPermanent(path, source string, userID uint64) (*Share, error)
	GetBySourcePath(path, source string) ([]*Share, error)
	Gets(path, source string, userID uint64) ([]*Share, error)
	Save(s *Share) error
	Delete(hash string) error
}

// crudBackend implements crud.CrudBackend[ShareInfo] for share storage.
type crudBackend struct {
	back StorageBackend
}

func (c *crudBackend) GetByID(id any) (*Share, error) {
	hash, ok := id.(string)
	if !ok {
		return nil, errors.ErrInvalidDataType
	}
	return c.back.GetByHash(hash)
}

func (c *crudBackend) GetAll() ([]*Share, error) {
	return c.back.All()
}

func (c *crudBackend) Save(obj *Share) error {
	return c.back.Save(obj)
}

func (c *crudBackend) DeleteByID(id any) error {
	hash, ok := id.(string)
	if !ok {
		return errors.ErrInvalidDataType
	}
	return c.back.Delete(hash)
}

// Storage is a share storage using generics.
type Storage struct {
	Generic     *crud.Storage[Share]
	back        StorageBackend
	shareByHash map[string]*Share            // key: link hash
	shareByPath map[string]map[string]string // key: pathKey(Source, Path), value: set of hashes (hash -> "")
	mu          sync.RWMutex
	users       *users.Storage
}

// pathKey returns the cache key for shareByPath (source + path).
func pathKey(source, path string) string {
	return source + ":" + path
}

// setCacheLocked updates both shareByHash and shareByPath for the given link.
// Caller must hold s.mu.
func (s *Storage) setCacheLocked(link *Share) {
	if link == nil {
		return
	}
	adjustedPath := utils.AddTrailingSlashIfNotExists(link.Path)
	adjustedSource := utils.AddTrailingSlashIfNotExists(link.Source)
	s.shareByHash[link.Hash] = link
	key := pathKey(adjustedSource, adjustedPath)
	if s.shareByPath[key] == nil {
		s.shareByPath[key] = make(map[string]string)
	}
	s.shareByPath[key][link.Hash] = ""
}

// setCache updates both caches for the given link. It acquires s.mu.
func (s *Storage) setCache(link *Share) {
	s.mu.Lock()
	s.setCacheLocked(link)
	s.mu.Unlock()
}

// deleteFromCacheLocked removes the link (by hash) from both caches.
// Caller must hold s.mu.
func (s *Storage) deleteFromCacheLocked(hash string) {
	link, ok := s.shareByHash[hash]
	if !ok {
		return
	}
	adjustedPath := utils.AddTrailingSlashIfNotExists(link.Path)
	adjustedSource := utils.AddTrailingSlashIfNotExists(link.Source)
	key := pathKey(adjustedSource, adjustedPath)
	if inner, ok := s.shareByPath[key]; ok {
		delete(inner, hash)
		if len(inner) == 0 {
			delete(s.shareByPath, key)
		}
	}
	delete(s.shareByHash, hash)
}

// deleteFromCache removes the link (by hash) from both caches. It acquires s.mu.
func (s *Storage) deleteFromCache(hash string) {
	s.mu.Lock()
	s.deleteFromCacheLocked(hash)
	s.mu.Unlock()
}

// ForgetShare drops the in-memory entry so the next GetByHash reloads from the backend
// (e.g. after state.RecordShareDownload updates the authoritative row).
func (s *Storage) ForgetShare(hash string) {
	s.deleteFromCache(hash)
}

// setCacheAfterMove updates both caches when a link's Source or Path changed. It acquires s.mu.
func (s *Storage) setCacheAfterMove(link *Share, oldSource, oldPath string) {
	s.mu.Lock()
	if link == nil {
		return
	}
	adjustedOldPath := utils.AddTrailingSlashIfNotExists(oldPath)
	adjustedOldSource := utils.AddTrailingSlashIfNotExists(oldSource)
	oldKey := pathKey(adjustedOldSource, adjustedOldPath)
	if inner, ok := s.shareByPath[oldKey]; ok {
		delete(inner, link.Hash)
		if len(inner) == 0 {
			delete(s.shareByPath, oldKey)
		}
	}
	s.setCacheLocked(link)
	s.mu.Unlock()
}

// NewStorage creates a share links storage from a backend and populates the
// in-memory cache from the database so all reads can be served from cache.
func NewStorage(back StorageBackend, usersStore *users.Storage) *Storage {
	s := &Storage{
		Generic:     crud.NewStorage[Share](&crudBackend{back: back}),
		back:        back,
		shareByHash: make(map[string]*Share),
		shareByPath: make(map[string]map[string]string),
		users:       usersStore,
	}
	s.loadShareCache()
	return s
}

// loadShareCache fills shareByHash and shareByPath from the backend and removes expired links from the DB.
// Call once at startup so all reads can be served from cache.
func (s *Storage) loadShareCache() {
	links, err := s.back.All()
	if err != nil && err != errors.ErrNotExist {
		return
	}
	if links == nil {
		links = []*Share{}
	}
	s.mu.Lock()
	for _, link := range links {
		if link == nil {
			continue
		}
		if link.Expire != 0 && link.Expire <= time.Now().Unix() && !link.KeepAfterExpiration {
			_ = s.back.Delete(link.Hash)
			continue
		}
		link.InitUserDownloads()
		s.setCacheLocked(link)
	}
	s.mu.Unlock()
}

// LoadShareCacheFromDB repopulates the in-memory cache from the database.
// Call at startup (e.g. from InitializeDb) to ensure shares are loaded after the store is ready.
func (s *Storage) LoadShareCacheFromDB() {
	s.loadShareCache()
}

// All returns all non-expired shares from the cache (populated at startup and by writes).
func (s *Storage) All() ([]*Share, error) {
	s.mu.Lock()
	result := make([]*Share, 0, len(s.shareByHash))
	for _, l := range s.shareByHash {
		if l == nil {
			continue
		}
		if l.Expire != 0 && l.Expire <= time.Now().Unix() && !l.KeepAfterExpiration {
			_ = s.back.Delete(l.Hash)
			s.deleteFromCacheLocked(l.Hash)
			continue
		}
		result = append(result, l)
	}
	s.mu.Unlock()
	return result, nil
}

// FindByUserID returns non-expired shares owned by userID from the cache.
func (s *Storage) FindByUserID(userID uint64) ([]*Share, error) {
	s.mu.Lock()
	result := make([]*Share, 0)
	for _, l := range s.shareByHash {
		if l == nil || l.UserID != userID {
			continue
		}
		if l.Expire != 0 && l.Expire <= time.Now().Unix() && !l.KeepAfterExpiration {
			_ = s.back.Delete(l.Hash)
			s.deleteFromCacheLocked(l.Hash)
			continue
		}
		result = append(result, l)
	}
	s.mu.Unlock()
	return result, nil
}

// GetByHash wraps StorageBackend.GetByHash and handles expiry.
func (s *Storage) GetByHash(hash string) (*Share, error) {
	// return stable in-memory pointer if available
	s.mu.RLock()
	if link, ok := s.shareByHash[hash]; ok && link != nil {
		s.mu.RUnlock()
		if link.Expire != 0 && link.Expire <= time.Now().Unix() {
			_ = s.back.Delete(hash)
			s.deleteFromCache(hash)
			return nil, errors.ErrNotExist
		}
		return link, nil
	}
	s.mu.RUnlock()

	link, err := s.back.GetByHash(hash)
	if err != nil {
		return nil, err
	}
	if link.Expire != 0 && link.Expire <= time.Now().Unix() {
		_ = s.back.Delete(hash)
		return nil, errors.ErrNotExist
	}

	// Initialize UserDownloads map
	link.InitUserDownloads()

	s.setCache(link)
	return link, nil
}

// GetPermanent wraps StorageBackend.GetPermanent
func (s *Storage) GetPermanent(path, source string, userID uint64) (*Share, error) {
	l, err := s.back.GetPermanent(path, source, userID)
	if err == nil && l != nil {
		s.setCache(l)
	}
	return l, err
}

// Gets returns shares for the given path, source, and owner user id from the cache.
func (s *Storage) Gets(sourcePath, source string, userID uint64) ([]*Share, error) {
	s.mu.Lock()
	adjustedPath := utils.AddTrailingSlashIfNotExists(sourcePath)
	adjustedSource := utils.AddTrailingSlashIfNotExists(source)
	key := pathKey(adjustedSource, adjustedPath)
	hashes := s.shareByPath[key]
	result := make([]*Share, 0, len(hashes))
	for h := range hashes {
		l := s.shareByHash[h]
		if l == nil || l.UserID != userID {
			continue
		}
		if l.Expire != 0 && l.Expire <= time.Now().Unix() && !l.KeepAfterExpiration {
			_ = s.back.Delete(l.Hash)
			s.deleteFromCacheLocked(l.Hash)
			continue
		}
		result = append(result, l)
	}
	s.mu.Unlock()
	return result, nil
}

// GetBySourcePath returns shares for the given path and source from the cache.
func (s *Storage) GetBySourcePath(path, source string) ([]*Share, error) {
	s.mu.Lock()
	adjustedPath := utils.AddTrailingSlashIfNotExists(path)
	adjustedSource := utils.AddTrailingSlashIfNotExists(source)
	key := pathKey(adjustedSource, adjustedPath)
	hashes := s.shareByPath[key]
	result := make([]*Share, 0, len(hashes))
	for h := range hashes {
		l := s.shareByHash[h]
		if l == nil {
			continue
		}
		if l.Expire != 0 && l.Expire <= time.Now().Unix() && !l.KeepAfterExpiration {
			_ = s.back.Delete(l.Hash)
			s.deleteFromCacheLocked(l.Hash)
			continue
		}
		result = append(result, l)
	}
	s.mu.Unlock()
	return result, nil
}

// IsShared returns whether the given path and source have any shares in the cache for owner userID.
func (s *Storage) IsShared(path, source string, userID uint64) bool {
	links, _ := s.GetBySourcePath(path, source)
	for _, l := range links {
		if l.UserID == userID {
			return true
		}
	}
	return len(links) > 0
}

// UpdateShares updates all shares that match oldSource and oldPath to point to newSource and newPath.
// Handles both exact matches and subdirectories, regardless of trailing slashes.
func (s *Storage) UpdateShares(oldSource, oldPath, newSource, newPath string) (int, error) {
	links, err := s.All()
	if err != nil && err != errors.ErrNotExist {
		logger.Error("failed to list shares", "error", err)
		return 0, err
	}

	// Normalize paths for comparison (remove trailing slashes)
	oldPath = utils.AddTrailingSlashIfNotExists(oldPath)
	newPath = utils.AddTrailingSlashIfNotExists(newPath)

	updated := 0
	for _, l := range links {
		if l == nil || l.Source != oldSource {
			continue
		}
		l.Path = utils.AddTrailingSlashIfNotExists(l.Path)

		pos := strings.Index(l.Path, oldPath)
		if pos < 0 {
			continue
		}

		l.Source = newSource
		l.Path = newPath

		if err := s.back.Save(l); err != nil {
			logger.Error("failed to save updated share", "hash", l.Hash, "error", err)
			return updated, err
		}

		s.setCacheAfterMove(l, oldSource, oldPath)
		updated++
	}
	return updated, nil
}

// UpdateSharePath updates the path for a specific share identified by hash
func (s *Storage) UpdateSharePath(hash, newPath string) error {
	link, err := s.GetByHash(hash)
	if err != nil {
		return err
	}

	oldPath := link.Path
	link.Path = newPath

	if err := s.back.Save(link); err != nil {
		logger.Error("failed to save updated share", "hash", hash, "error", err)
		return err
	}

	s.setCacheAfterMove(link, link.Source, oldPath)

	logger.Debug("share path updated", "hash", hash, "fromPath", oldPath, "toPath", newPath)
	return nil
}

// CreateShare creates a new share and updates the cache
// Returns an error if the share already exists
func (s *Storage) CreateShare(l *Share) error {
	// 1. Check if share already exists in cache (state)
	s.mu.RLock()
	_, existsInCache := s.shareByHash[l.Hash]
	s.mu.RUnlock()

	if existsInCache {
		return fmt.Errorf("share with hash %s already exists", l.Hash)
	}

	// 2. Update database
	if err := s.back.Save(l); err != nil {
		return err
	}

	// 3. Update cache to match database
	s.setCache(l)
	return nil
}

// UpdateShare updates an existing share and updates the cache
// Returns an error if the share doesn't exist
func (s *Storage) UpdateShare(l *Share) error {
	// 1. Check if share exists in cache (state)
	s.mu.RLock()
	_, existsInCache := s.shareByHash[l.Hash]
	s.mu.RUnlock()

	if !existsInCache {
		return fmt.Errorf("share with hash %s not found in cache", l.Hash)
	}

	// 2. Update database
	if err := s.back.Save(l); err != nil {
		return err
	}

	// 3. Update cache to match database
	s.setCache(l)
	return nil
}

// Delete wraps StorageBackend.Delete
func (s *Storage) Delete(hash string) error {
	if err := s.back.Delete(hash); err != nil {
		return err
	}
	s.deleteFromCache(hash)
	return nil
}

// Flush is a no-op: every share write
// updates the database, so the cache and DB stay in sync without flushing.
func (s *Storage) Flush() error {
	return nil
}

// GetShareInfoByHash returns share presentation fields for a hash (e.g. public/info).
func (s *Storage) GetShareInfoByHash(hash string) (*FrontendShareInfo, error) {
	return s.back.GetShareInfoByHash(hash)
}
