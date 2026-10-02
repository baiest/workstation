// Package safeio reads small local files without trusting their size.
package safeio

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// ErrTooLarge means a file is bigger than the limit the caller accepts.
var ErrTooLarge = errors.New("file too large")

// ReadFile reads path, refusing anything larger than max bytes. The limit is
// enforced while reading, so a file that grows after Stat cannot slip through.
func ReadFile(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%s: %w (limit %d bytes)", path, ErrTooLarge, max)
	}
	return data, nil
}
