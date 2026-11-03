//go:build windows

package cmd

import (
	"errors"
	"io"
)

func openRexTTY() (io.WriteCloser, error) {
	return nil, errors.New("Rex speech status requires a Unix terminal")
}
