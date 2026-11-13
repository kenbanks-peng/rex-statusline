package display

import (
	"strings"
	"testing"
)

func TestMouseInput(t *testing.T) {
	var mouse mouseInput
	if events, stop := mouse.feed([]byte("typing\x1b[<0;10;")); len(events) != 0 || stop {
		t.Fatal(events, stop)
	}
	events, stop := mouse.feed([]byte("20M\x1b[<32;-5;40M\x1b[<32;6;41M\x1b[<0;1;1m"))
	want := []mouseEvent{{button: 0}, {button: 32}, {button: 0, released: true}}
	if stop || len(events) != len(want) {
		t.Fatal(events, stop)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatal(events)
		}
	}
	if events, _ = mouse.feed([]byte(strings.Repeat("x", 100000))); len(events) != 0 || len(mouse.buffer) > 64 {
		t.Fatal("unbounded input")
	}
}

func TestResizeInput(t *testing.T) {
	var input mouseInput
	for _, chunk := range []string{"\x1b[48;", "2;80;0;", "0t"} {
		events, stop := input.feed([]byte(chunk))
		if len(events) != 0 || stop {
			t.Fatal(events, stop)
		}
	}
	if !input.resized {
		t.Fatal("fragmented resize report lost")
	}
	events, stop := input.feed([]byte("\x1b[48;3;40;600;800t\x1b[<0;1;1M"))
	if !input.resized || len(events) != 1 || stop {
		t.Fatal(input.resized, events, stop)
	}
	input.feed(nil)
	if input.resized {
		t.Fatal("resize report replayed")
	}
	input.feed([]byte("\x1b[200~\x1b[48;2;80;0;0t\x1b[201~"))
	if input.resized {
		t.Fatal("pasted resize report accepted")
	}
	input.feed([]byte("\x1b[48;2;80t"))
	if input.resized {
		t.Fatal("malformed resize report accepted")
	}
}

func TestPasteCannotDragOrInterrupt(t *testing.T) {
	var mouse mouseInput
	for _, data := range []string{"\x1b[20", "0~\x03\x1b[<0;1;1M", "\x1b[<32;2;2M\x1b[201~"} {
		if events, stop := mouse.feed([]byte(data)); len(events) != 0 || stop {
			t.Fatal(events, stop)
		}
	}
	if events, stop := mouse.feed([]byte("\x1b[<0;3;3M")); len(events) != 1 || stop {
		t.Fatal(events, stop)
	}
	if _, stop := mouse.feed([]byte{3}); !stop {
		t.Fatal("Ctrl+C ignored")
	}
}
