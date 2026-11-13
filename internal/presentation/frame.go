// Package presentation carries a complete display update in one frame.
package presentation

import (
	"encoding/json"
	"rex-statusline/internal/timing"
)

type Frame struct {
	Line    string        `json:"line"`
	Columns int           `json:"columns"`
	Trace   *timing.Trace `json:"trace,omitempty"`
}

func Encode(line string, columns int, trace *timing.Trace) (string, error) {
	data, err := json.Marshal(Frame{Line: line, Columns: columns, Trace: trace})
	return string(data), err
}
