package config

import (
	"bytes"
	"os"

	"github.com/BurntSushi/toml"
)

// Save writes the config back to disk. When AgentPriority is set, it syncs
// DefaultAgent to priority[0] for backwards compatibility.
func Save(loaded Loaded) error {
	if len(loaded.Config.Project.AgentPriority) > 0 {
		loaded.Config.Project.DefaultAgent = loaded.Config.Project.AgentPriority[0]
	}

	var buf bytes.Buffer
	encoder := toml.NewEncoder(&buf)
	if err := encoder.Encode(loaded.Config); err != nil {
		return err
	}

	return os.WriteFile(loaded.Path, buf.Bytes(), 0644)
}
