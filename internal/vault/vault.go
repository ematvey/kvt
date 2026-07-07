package vault

import (
	"os"
	"path/filepath"

	"github.com/ematvey/kvt/internal/frontmatter"
	"github.com/ematvey/kvt/internal/pathutil"
)

type Concept struct {
	Path     pathutil.Path
	Document frontmatter.Document
}

func ReadConcept(root string, conceptPath pathutil.Path) (Concept, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(conceptPath.String())))
	if err != nil {
		return Concept{}, err
	}
	doc, err := frontmatter.Parse(data)
	if err != nil {
		return Concept{}, err
	}
	return Concept{
		Path:     conceptPath,
		Document: doc,
	}, nil
}
