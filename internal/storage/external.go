package storage

// ExternalStorage implements storage for user-provided data.
// Currently wraps LocalStorage, but can be extended to support S3/MinIO.
type ExternalStorage struct {
	*LocalStorage
}

// NewExternalStorage creates storage for external user data.
func NewExternalStorage(baseDir string) (*ExternalStorage, error) {
	local, err := NewLocalStorage(baseDir)
	if err != nil {
		return nil, err
	}
	return &ExternalStorage{LocalStorage: local}, nil
}

// All methods inherited from LocalStorage:
// - Store(ctx, name, content) (key, error) - generates random keys
// - StoreWithKey(ctx, key, content) error - stores with specific key
// - Get(ctx, key) ([]byte, error) - returns content directly
// - Delete(ctx, key) error - removes content
//
// Future: Override Get() to download from S3 if needed.
