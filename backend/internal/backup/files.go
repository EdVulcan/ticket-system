package backup

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func copyFile(source, target string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return err
	}
	if err := output.Sync(); err != nil {
		_ = output.Close()
		return err
	}
	return output.Close()
}

func copyKeyFile(source, target string) error {
	if strings.TrimSpace(source) == "" {
		return nil
	}
	absSource, err := filepath.Abs(source)
	if err != nil {
		return fmt.Errorf("resolve security key file: %w", err)
	}
	data, err := os.ReadFile(absSource)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read security key file: %w", err)
	}
	if err := os.WriteFile(target, data, 0600); err != nil {
		return fmt.Errorf("back up security key file: %w", err)
	}
	return nil
}
