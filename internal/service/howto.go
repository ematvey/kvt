package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/ematvey/kvt/internal/pathutil"
)

func (s *Service) HouseHowto(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(s.root, pathutil.HouseHowtoPath))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
