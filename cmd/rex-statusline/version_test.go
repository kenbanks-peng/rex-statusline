package main

import (
	"context"
	"io"
	"os"
	"testing"
)

func TestVersion(t *testing.T) {
	original := version
	defer func() { version = original }()
	for _, value := range []string{"dev", "v1.2.3", "v1.2.3-rc.1"} {
		for _, command := range []string{"version", "--version"} {
			t.Run(value+"/"+command, func(t *testing.T) {
				version = value
				r, w, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				defer r.Close()
				defer w.Close()
				stdout := os.Stdout
				os.Stdout = w
				defer func() { os.Stdout = stdout }()

				runErr := run(context.Background(), []string{command})
				w.Close()
				output, err := io.ReadAll(r)
				if err != nil {
					t.Fatal(err)
				}
				if runErr != nil {
					t.Fatal(runErr)
				}
				if string(output) != value+"\n" {
					t.Fatalf("got %q, want %q", output, value+"\n")
				}
			})
		}
	}
}

func TestVersionArguments(t *testing.T) {
	for _, command := range []string{"version", "--version"} {
		for _, argument := range []string{"extra", "--unknown"} {
			if err := run(context.Background(), []string{command, argument}); err == nil {
				t.Fatalf("%s %s should fail", command, argument)
			}
		}
	}
}
