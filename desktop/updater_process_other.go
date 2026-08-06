//go:build !windows

package main

import "errors"

func startUpdateScript(string) error {
	return errors.New("desktop updates are only supported on Windows")
}
