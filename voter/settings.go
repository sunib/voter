package main

// The menu and keys from a folder, for a deployment that reads nothing from
// Kubernetes (SETTINGS_DIR, docs/voter-settings-mode-plan.md).
//
// The folder knows nothing about where it came from: an image volume, a
// ConfigMap or a directory on a laptop all read the same. What a file is comes
// from its content and never its name, because a recreated Secret comes back as
// voter-<name>.sops.yaml.
//
// It is read once. An image volume never changes under a pod, so a new menu is a
// new pod, and the browser finds out through the revision below.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

const (
	menuSourceKubernetes = "kubernetes"
	menuSourceFolder     = "folder"
)

// settings is what this pod loaded from SETTINGS_DIR. Set once before serving
// and never changed, so readers need no lock.
type settings struct {
	menu coffeeConfig
	// revision is a hash of the menu file's bytes, and of nothing else: a new
	// key changes nothing a phone shows, and nothing derived from a key is ever
	// shown (docs/coffee-api-keys-design.md).
	revision string
	// reference is SETTINGS_REFERENCE, for people to read. Nothing depends on it.
	reference string
	// keys is nil when SETTINGS_KEYS is empty.
	keys *settingsKeys
}

// settingsFileHead is the part of a file that says what the file is. In an
// encrypted Secret it is readable: sops encrypts data and leaves the rest.
type settingsFileHead struct {
	Kind     string `json:"kind"`
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Sops any `json:"sops"`
}

// loadSettings is strict about the menu and relaxed about everything else: a
// stray file must not take down the coffee bar, and a bad menu must not reach
// one, which is why it returns an error for main to exit on. With
// maxUnavailable 0, the old pod keeps serving.
func loadSettings(cfg config) (*settings, error) {
	paths, err := filepath.Glob(filepath.Join(cfg.SettingsDir, "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("SETTINGS_DIR=%q: %w", cfg.SettingsDir, err)
	}

	loaded := &settings{reference: cfg.SettingsReference}
	var menuFile string
	keyFiles := map[string]string{}

	for _, path := range paths {
		raw, err := os.ReadFile(path) // #nosec G304 -- from the configured folder
		if err != nil {
			log.Printf("settings: skipping %s: %v", path, err)
			continue
		}
		var head settingsFileHead
		if err := yaml.Unmarshal(raw, &head); err != nil {
			// The parser's message quotes the file, and this may be a Secret.
			log.Printf("settings: skipping %s: not YAML", path)
			continue
		}
		switch {
		case head.Kind == "CoffeeConfig" && head.Metadata.Name == cfg.CoffeeConfigName:
			if menuFile != "" {
				return nil, fmt.Errorf("SETTINGS_DIR=%q: both %s and %s are the menu %q", cfg.SettingsDir, menuFile, path, cfg.CoffeeConfigName)
			}
			menu, err := parseMenu(raw)
			if err != nil {
				return nil, fmt.Errorf("menu %s: %w", path, err)
			}
			sum := sha256.Sum256(raw)
			loaded.menu, loaded.revision = menu, hex.EncodeToString(sum[:])[:16]
			menuFile = path
		case head.Kind == "Secret" && head.Sops != nil:
			if earlier, ok := keyFiles[head.Metadata.Name]; ok {
				log.Printf("settings: both %s and %s are the Secret %s; using the second", earlier, path, head.Metadata.Name)
			}
			keyFiles[head.Metadata.Name] = path
		case head.Kind == "Secret":
			log.Printf("settings: skipping %s: a Secret that is not encrypted is never held", path)
		default:
			log.Printf("settings: skipping %s: not the menu and not an encrypted Secret", path)
		}
	}
	if menuFile == "" {
		return nil, fmt.Errorf("SETTINGS_DIR=%q holds no CoffeeConfig named %q, in %d YAML file(s)", cfg.SettingsDir, cfg.CoffeeConfigName, len(paths))
	}

	credentials, err := newSopsCredentials(cfg)
	if err != nil {
		return nil, err
	}
	if credentials != nil {
		loaded.keys = newSettingsKeys(keyFiles, credentials)
	}
	return loaded, nil
}

// parseMenu checks what would make every order fail. The CRD's other rules are
// not repeated here.
func parseMenu(raw []byte) (coffeeConfig, error) {
	var menu coffeeConfig
	if err := yaml.Unmarshal(raw, &menu); err != nil {
		return menu, err
	}
	if menu.Spec.Currency == "" {
		return menu, errors.New("it has no currency")
	}
	if len(menu.Spec.Products) == 0 {
		return menu, errors.New("it has no products")
	}
	return menu, nil
}

// menuRevision is what the browser compares, and empty when the menu is from
// Kubernetes, where the stream does this job.
func (s *settings) menuRevision() string {
	if s == nil {
		return ""
	}
	return s.revision
}

func (s *settings) menuSource() string {
	if s == nil {
		return menuSourceKubernetes
	}
	return menuSourceFolder
}
