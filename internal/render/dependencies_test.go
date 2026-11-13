package render

import (
	"encoding/json"
	"strings"
	"testing"

	"rex-statusline/internal/defaults"

	"github.com/pelletier/go-toml/v2"
)

func TestThemeDependencyPolicy(t *testing.T) {
	for _, tt := range []struct {
		name, segment, source string
		want                  nativeDependencies
		cacheable             bool
	}{
		{"literal", "text", "literal", 0, true},
		{"input", "text", "{{ if eq .Env.BSP_USER_ID \"0\" }}root{{ end }}", 0, true},
		{"git", "text", "{{ .Env.GITSTATUS }}{{ .Env.GITSTATUS_CHANGED }}", nativeGit, true},
		{"languages", "text", "{{ .Env.JS_SEGMENT }}{{ .Env.PYTHON_SEGMENT }}", nativeJavaScript | nativePython, true},
		{"host", "text", "{{ .Env.POSH_HOST_LABEL }}", nativeHost, true},
		{"status", "status", "{{ if .Error }}{{ .Meaning }}{{ else }}{{ .Code }}{{ end }}", 0, true},
		{"duration", "executiontime", "{{ .FormattedMs }}", 0, true},
		{"clock segment", "time", "clock", allNative, false},
		{"clock function", "text", "{{ now }}", allNative, false},
		{"file function", "text", "{{ readFile \"/tmp/state\" }}", allNative, false},
		{"command", "command", "command", allNative, false},
		{"global clock", "text", "{{ .CurrentDate }}", allNative, false},
		{"global context", "text", "{{ . }}", allNative, false},
		{"environment object", "text", "{{ .Env }}", allNative, false},
		{"method", "text", "{{ .Env.FOO.Method }}", allNative, false},
		{"scope change", "text", "{{ with .Env }}{{ .GITSTATUS }}{{ end }}", allNative, false},
		{"indirect", "text", "{{ index .Env \"GITSTATUS\" }}", allNative, false},
		{"filesystem segment", "go", "{{ .Full }}", 0, false},
		{"os files", "os", "{{ .Icon }}", 0, false},
		{"lua files", "lua", "{{ .Full }}", 0, false},
		{"zig files", "zig", "{{ .Full }}", 0, false},
		{"docker files", "docker", "{{ .Context }}", 0, false},
		{"os native host", "os", "{{ .Icon }}{{ .Env.POSH_HOST_LABEL }}", nativeHost, false},
		{"go native git", "go", "{{ .Full }}{{ .Env.GITSTATUS }}", nativeGit, false},
		{"builtin file function", "go", "{{ readFile \"/tmp/state\" }}", allNative, false},
		{"unrecognized builtin field", "os", "{{ .Unknown }}", allNative, false},
		{"unrecognized text field", "text", "{{ .Full }}", allNative, false},
		{"definition", "text", "{{ define \"hidden\" }}hidden{{ end }}", allNative, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			theme := map[string]any{"version": 4, "blocks": []any{map[string]any{
				"type": "prompt", "segments": []any{map[string]any{"type": tt.segment, "template": tt.source}},
			}}}
			deps, cacheable := themeDependencies(theme)
			if deps != tt.want || cacheable != tt.cacheable {
				t.Fatalf("got %v, %v; want %v, %v", deps, cacheable, tt.want, tt.cacheable)
			}
		})
	}
}

func TestUnknownThemeFeaturesDisableCaching(t *testing.T) {
	for _, theme := range []map[string]any{
		{"version": 4, "transient_prompt": map[string]any{"template": "{{ now }}"}},
		{"version": 4, "blocks": []any{map[string]any{"segments": []any{map[string]any{"template": "missing type"}}}}},
		{"version": 4, "blocks": []any{map[string]any{"segments": []any{map[string]any{"type": "text", "options": map[string]any{"unknown": true}}}}}},
	} {
		if _, cacheable := themeDependencies(theme); cacheable {
			t.Fatalf("unrecognized feature accepted: %#v", theme)
		}
	}
}

func TestBundledThemeKeepsOpaqueDependenciesFresh(t *testing.T) {
	var theme map[string]any
	if err := toml.Unmarshal(defaults.Theme, &theme); err != nil {
		t.Fatal(err)
	}
	if deps, cacheable := themeDependencies(theme); cacheable || deps != allNative {
		t.Fatal("bundled language, OS and Docker dependencies must not be cached")
	}
}

func TestBundledBuiltinSegmentsDoNotForceAllNativeProbes(t *testing.T) {
	for _, tt := range []struct {
		name, reference string
		removed         nativeDependencies
	}{
		{"host", ".Env.POSH_HOST_LABEL", nativeHost},
		{"python", ".Env.PYTHON_SEGMENT", nativePython},
		{"javascript", ".Env.JS_SEGMENT", nativeJavaScript},
		{"git", ".Env.GITSTATUS", nativeGit},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var theme map[string]any
			if err := toml.Unmarshal(defaults.Theme, &theme); err != nil {
				t.Fatal(err)
			}
			// Keep all other shipped segments, including the OMP built-ins.
			// Replacing just this segment's templates removes its native reads.
			data, err := json.Marshal(theme)
			if err != nil {
				t.Fatal(err)
			}
			var normalized map[string]any
			if err := json.Unmarshal(data, &normalized); err != nil {
				t.Fatal(err)
			}
			for _, entry := range normalized["blocks"].([]any) {
				for _, entry := range entry.(map[string]any)["segments"].([]any) {
					segment := entry.(map[string]any)
					for key, value := range segment {
						data, _ := json.Marshal(value)
						if strings.Contains(string(data), tt.reference) {
							delete(segment, key)
						}
					}
				}
			}
			deps, cacheable := themeDependencies(normalized)
			if deps != allNative&^tt.removed || cacheable {
				t.Fatalf("got deps %v cacheable %v; want deps %v false", deps, cacheable, allNative&^tt.removed)
			}
		})
	}
}

func TestOpaqueFeaturesKeepAllNativeProbesAlongsideAuditedBuiltins(t *testing.T) {
	for _, opaque := range []map[string]any{
		{"type": "command", "options": map[string]any{"script": "echo $GITSTATUS"}},
		{"type": "time"},
		{"type": "text", "template": "{{ readFile \"/tmp/state\" }}"},
		{"type": "go", "options": map[string]any{"tools": []any{map[string]any{"command": "custom"}}}},
	} {
		theme := map[string]any{"version": 4, "blocks": []any{map[string]any{
			"type": "prompt", "segments": []any{map[string]any{"type": "go", "template": "{{ .Full }}"}, opaque},
		}}}
		if deps, cacheable := themeDependencies(theme); deps != allNative || cacheable {
			t.Fatalf("opaque feature lost fallback: %v, %v; %#v", deps, cacheable, opaque)
		}
	}
}
