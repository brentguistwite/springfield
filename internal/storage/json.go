package storage

import (
	"encoding/json"
	"fmt"
	"os"
)

// ReadJSON decodes a runtime JSON file into target.
func (r Runtime) ReadJSON(path string, target any) error {
	fullPath, err := r.Path(path)
	if err != nil {
		return err
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return fmt.Errorf("read runtime json %s: %w", fullPath, err)
	}

	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode runtime json %s: %w", fullPath, err)
	}

	return nil
}

// WriteJSON writes a generated runtime file as JSON.
func (r Runtime) WriteJSON(path string, value any) error {
	fullPath, err := r.Path(path)
	if err != nil {
		return err
	}

	if err := r.ensureParent(fullPath); err != nil {
		return err
	}

	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode runtime json %s: %w", fullPath, err)
	}

	data = append(data, '\n')
	if err := os.WriteFile(fullPath, data, 0o644); err != nil {
		return fmt.Errorf("write runtime json %s: %w", fullPath, err)
	}

	return nil
}
