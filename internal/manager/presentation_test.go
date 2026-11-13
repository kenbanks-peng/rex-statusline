package manager

import (
	"encoding/json"
	"testing"

	"rex-statusline/internal/presentation"
)

func frameLine(t *testing.T, data []byte) string {
	t.Helper()
	var frame presentation.Frame
	if err := json.Unmarshal(data, &frame); err != nil {
		t.Fatal(err)
	}
	return frame.Line
}
