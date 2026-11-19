package cli

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/baba983/vscroxy/internal/certs"
	"github.com/baba983/vscroxy/internal/datadir"
)

// loadCertificates reads the desktop's certificate configuration and sealed
// passphrases. A headless run uses the same client certificates and roots as
// Send and the desktop Runner. No settings file means no certificates.
func loadCertificates() (certs.Settings, error) {
	path, err := datadir.Path("settings.json")
	if err != nil {
		return certs.Settings{}, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return certs.Settings{}, nil
	}
	if err != nil {
		return certs.Settings{}, fmt.Errorf("read certificate settings %s: %w", path, err)
	}
	var file struct {
		Certificates certs.Settings `json:"certificates"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return certs.Settings{}, fmt.Errorf("read certificate settings %s: %w", path, err)
	}
	settings := file.Certificates.Normalize()
	if !settings.Configured() {
		return settings, nil
	}
	secrets, err := certs.SecretStoreFor(path).Load()
	if err != nil {
		return certs.Settings{}, err
	}
	settings.Secrets = secrets
	if err := settings.Validate(); err != nil {
		return certs.Settings{}, fmt.Errorf("certificate settings: %w", err)
	}
	return settings, nil
}
