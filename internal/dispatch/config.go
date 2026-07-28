package dispatch

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
)

func DefaultConfig() Config {
	return Config{
		Limits: Limits{
			GlobalOpenPRs:         3,
			PerRepo:               1,
			MaxDispatchesPerNight: 5,
		},
		Schedule: Schedule{DispatchAt: "22:00", RetryUntil: "06:00"},
	}
}

// LoadConfig reads path over the defaults. A missing file is not an error —
// the zero-config case must work. Unmarshalling into an already-populated
// struct is what keeps unset keys at their defaults rather than zero.
func LoadConfig(path string) (Config, error) {
	c := DefaultConfig()
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return DefaultConfig(), err
	}
	return c, nil
}

// LimitFor returns the per-repo concurrency limit for a bare repo name.
func (c Config) LimitFor(repo string) int {
	if n, ok := c.Limits.Overrides[repo]; ok {
		return n
	}
	return c.Limits.PerRepo
}
