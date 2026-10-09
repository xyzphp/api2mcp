package envfile

import (
	"bufio"
	"os"
	"strings"
)

// Load a local development .env without overwriting container / process env.
func Load(filename string) {
	file, err := os.Open(filename)
	if err != nil {
		return
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, strings.Trim(value, "\"'"))
		}
	}
}
