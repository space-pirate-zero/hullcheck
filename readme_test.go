package hullcheck_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// localRef matches the images and links a Markdown page points at: <img src="..">
// and ![alt](..) / [text](..).
var localRef = regexp.MustCompile(`(?:src="([^"]+)"|\]\(([^)\s#]+)(?:#[^)]*)?\))`)

// A README that points at a file the repository does not contain renders a broken
// image on the project's front page. brand/wordmark.png was referenced for weeks and
// never committed: the generator wrote it, nobody added it, and nothing looked.
func TestReadmeReferencesExist(t *testing.T) {
	for _, doc := range []string{"README.md", "brand/BRAND.md", "SECURITY.md", "CONTRIBUTING.md"} {
		body, err := os.ReadFile(doc)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range localRef.FindAllStringSubmatch(string(body), -1) {
			ref := m[1] + m[2]
			if ref == "" || strings.Contains(ref, "://") || strings.HasPrefix(ref, "mailto:") {
				continue
			}
			p := filepath.Join(filepath.Dir(doc), filepath.FromSlash(ref))
			if _, err := os.Stat(p); err != nil {
				t.Errorf("%s points at %s, which is not in the repository", doc, ref)
			}
		}
	}
}
