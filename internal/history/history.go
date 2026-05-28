package history

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mdzunic/cli-agent/internal/ollama"
)

type Session struct {
	ID        string           `json:"id"`
	Name      string           `json:"name,omitempty"`
	Model     string           `json:"model"`
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
	Messages  []ollama.Message `json:"messages"`
}

func sessionsDir() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cli-agent", "sessions"), nil
}

func Save(model string, messages []ollama.Message, name string) (string, error) {
	dir, err := sessionsDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	now := time.Now()
	id := now.Format("2006-01-02-150405")
	s := Session{
		ID:        id,
		Name:      name,
		Model:     model,
		CreatedAt: now,
		UpdatedAt: now,
		Messages:  messages,
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", err
	}

	path := filepath.Join(dir, id+".json")
	return id, os.WriteFile(path, data, 0o644)
}

func Update(id string, messages []ollama.Message, model string) error {
	dir, err := sessionsDir()
	if err != nil {
		return err
	}

	path := filepath.Join(dir, id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("session %s not found", id)
	}

	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}

	s.Messages = messages
	if model != "" {
		s.Model = model
	}
	s.UpdatedAt = time.Now()

	out, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

func Rename(id string, name string) error {
	dir, err := sessionsDir()
	if err != nil {
		return err
	}

	path := filepath.Join(dir, id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("session %s not found", id)
	}

	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}

	s.Name = name
	s.UpdatedAt = time.Now()

	out, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

// Load finds a session by exact ID, ID prefix, or name prefix.
func Load(query string) (*Session, error) {
	dir, err := sessionsDir()
	if err != nil {
		return nil, err
	}

	// Try exact ID match first.
	path := filepath.Join(dir, query+".json")
	if data, err := os.ReadFile(path); err == nil {
		var s Session
		if err := json.Unmarshal(data, &s); err == nil {
			return &s, nil
		}
	}

	// Search all sessions for ID prefix or name prefix match.
	sessions, err := List()
	if err != nil {
		return nil, fmt.Errorf("session %q not found", query)
	}

	var matches []Session
	for _, s := range sessions {
		if strings.HasPrefix(s.ID, query) {
			matches = append(matches, s)
		} else if s.Name != "" && strings.HasPrefix(strings.ToLower(s.Name), strings.ToLower(query)) {
			matches = append(matches, s)
		}
	}

	if len(matches) == 1 {
		return &matches[0], nil
	}
	if len(matches) > 1 {
		names := make([]string, len(matches))
		for i, m := range matches {
			if m.Name != "" {
				names[i] = fmt.Sprintf("%s (%s)", m.Name, m.ID)
			} else {
				names[i] = m.ID
			}
		}
		return nil, fmt.Errorf("multiple sessions match %q: %s", query, strings.Join(names, ", "))
	}

	return nil, fmt.Errorf("session %q not found", query)
}

func Delete(id string) error {
	dir, err := sessionsDir()
	if err != nil {
		return err
	}

	path := filepath.Join(dir, id+".json")
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("session %s not found", id)
	}
	return nil
}

func List() ([]Session, error) {
	dir, err := sessionsDir()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var sessions []Session
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var s Session
		if err := json.Unmarshal(data, &s); err != nil {
			continue
		}
		sessions = append(sessions, s)
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	return sessions, nil
}
