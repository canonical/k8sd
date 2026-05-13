package preflight

import (
	"os"
	"path/filepath"
	"strings"
)

// CurrentComponents returns components from the installed snap by reading $SNAP/images.txt.
func CurrentComponents() []ComponentInfo {
	snapDir := os.Getenv("SNAP")
	if snapDir == "" {
		return nil
	}
	return readComponentsFromFile(filepath.Join(snapDir, "images.txt"))
}

// ParseImageLines parses raw image strings (one per line) into ComponentInfo list.
func ParseImageLines(lines []string) []ComponentInfo {
	return parseImageList(lines)
}

func readComponentsFromFile(path string) []ComponentInfo {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return parseImageList(strings.Split(strings.TrimSpace(string(data)), "\n"))
}

func parseImageList(lines []string) []ComponentInfo {
	var result []ComponentInfo
	seen := make(map[string]bool)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		name := parts[0]
		tag := parts[1]

		if seen[name] {
			continue
		}
		seen[name] = true

		result = append(result, ComponentInfo{
			Name:    name,
			Version: tag,
		})
	}
	return result
}
