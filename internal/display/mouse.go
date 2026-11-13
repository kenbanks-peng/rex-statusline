package display

import (
	"bytes"
	"regexp"
	"strconv"
)

type mouseEvent struct {
	button   int
	released bool
}

var mouseToken = regexp.MustCompile("\x1b\\[(?:<([0-9]{1,5});-?[0-9]{1,9};-?[0-9]{1,9}([Mm])|(20[01])~|(48;[0-9]{1,9};[0-9]{1,9};[0-9]{1,9};[0-9]{1,9})t)")

type mouseInput struct {
	buffer  []byte
	pasting bool
	resized bool
}

func (m *mouseInput) feed(data []byte) ([]mouseEvent, bool) {
	m.resized = false
	m.buffer = append(m.buffer, data...)
	var events []mouseEvent
	end := 0
	interrupt := false
	for _, match := range mouseToken.FindAllSubmatchIndex(m.buffer, -1) {
		if !m.pasting && bytes.IndexByte(m.buffer[end:match[0]], 3) >= 0 {
			interrupt = true
		}
		if match[6] >= 0 {
			m.pasting = string(m.buffer[match[6]:match[7]]) == "200"
		} else if match[8] >= 0 {
			m.resized = m.resized || !m.pasting
		} else if !m.pasting {
			button, _ := strconv.Atoi(string(m.buffer[match[2]:match[3]]))
			event := mouseEvent{button: button, released: m.buffer[match[4]] == 'm'}
			if len(events) > 0 && event.button == 32 && !event.released && events[len(events)-1] == event {
				end = match[1]
				continue
			}
			events = append(events, event)
		}
		end = match[1]
	}
	if !m.pasting && bytes.IndexByte(m.buffer[end:], 3) >= 0 {
		interrupt = true
	}
	m.buffer = append([]byte(nil), m.buffer[max(end, len(m.buffer)-64):]...)
	return events, interrupt
}
