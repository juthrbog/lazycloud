package aws

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ListProfiles reads AWS profile names from ~/.aws/config and ~/.aws/credentials.
// Returns a sorted, deduplicated list.
func ListProfiles() []string {
	seen := make(map[string]bool)

	// ~/.aws/config uses [profile name] (except [default])
	for _, name := range parseConfigFile(configPath(), true) {
		seen[name] = true
	}

	// ~/.aws/credentials uses [name] directly
	for _, name := range parseConfigFile(credentialsPath(), false) {
		seen[name] = true
	}

	profiles := make([]string, 0, len(seen))
	for name := range seen {
		profiles = append(profiles, name)
	}
	sort.Strings(profiles)
	return profiles
}

func configPath() string {
	if v := os.Getenv("AWS_CONFIG_FILE"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".aws", "config")
}

func credentialsPath() string {
	if v := os.Getenv("AWS_SHARED_CREDENTIALS_FILE"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".aws", "credentials")
}

// parseConfigFile extracts profile names from an INI-style AWS config file.
// If profilePrefix is true, sections are expected as [profile name] (AWS config format).
// If false, sections are plain [name] (credentials format).
func parseConfigFile(path string, profilePrefix bool) []string {
	if path == "" {
		return nil
	}

	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var profiles []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "[") || !strings.HasSuffix(line, "]") {
			continue
		}
		section := line[1 : len(line)-1]
		section = strings.TrimSpace(section)

		if profilePrefix {
			// [default] is a special case — no "profile " prefix
			if section == "default" {
				profiles = append(profiles, "default")
			} else if strings.HasPrefix(section, "profile ") {
				name := strings.TrimSpace(strings.TrimPrefix(section, "profile "))
				if name != "" {
					profiles = append(profiles, name)
				}
			}
		} else {
			if section != "" {
				profiles = append(profiles, section)
			}
		}
	}
	return profiles
}
