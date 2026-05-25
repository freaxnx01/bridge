package store

// Lock is a held file lock. Call Release exactly once.
type Lock interface {
	Release() error
}
