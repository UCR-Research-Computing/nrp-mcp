package plan

import (
	"os"
	"path/filepath"
)

func readFile(root, rel string) string {
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return ""
	}
	if len(b) > 256*1024 {
		b = b[:256*1024]
	}
	return string(b)
}
