package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/katachinosekai/quill/internal/kmemrelease"
)

func verifyKmemInstallation(path string) error {
	if _, err := kmemrelease.VerifyInstalled(path); err != nil {
		return fmt.Errorf("kmem signature verification failed: %w", err)
	}
	return nil
}

func verifiedKmemBin() (string, error) {
	path := kmemBin()
	if path == "" {
		return "", errors.New("kmem is not installed")
	}
	if err := verifyKmemInstallation(path); err != nil {
		return "", err
	}
	return path, nil
}

func kmemGuardSibling(path string) string {
	name := "kmem-guard"
	if strings.EqualFold(filepath.Ext(path), ".exe") {
		name += ".exe"
	}
	return filepath.Join(filepath.Dir(path), name)
}
