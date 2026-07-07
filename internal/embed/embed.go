package embed

import (
	"context"
	"fmt"
)

type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

type ConfigError struct {
	Message string
}

func (e *ConfigError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return e.Message
}

func validateDimensions(vectors [][]float32, dimensions int) error {
	if dimensions <= 0 {
		return nil
	}
	for i, vector := range vectors {
		if len(vector) != dimensions {
			return fmt.Errorf("embedding %d dimensions = %d, want %d", i, len(vector), dimensions)
		}
	}
	return nil
}
