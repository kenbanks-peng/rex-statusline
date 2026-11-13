package manager

import (
	"context"
	"errors"
	"testing"

	"rex-statusline/internal/rex"
)

func TestPrepareUsesFreshDimensionsWithoutSeparateSizeRead(t *testing.T) {
	m := Manager{API: apiFunc(func(context.Context, string, string, any, any) error {
		t.Fatal("separate size read")
		return nil
	}), Prepare: func(context.Context, Source) (rex.Size, RenderFunc, error) {
		return rex.Size{Columns: 80, Rows: 24}, func(_ context.Context, source Source) (string, error) {
			if source.Size.Columns != 160 {
				t.Errorf("columns %d", source.Size.Columns)
			}
			return "fresh", nil
		}, nil
	}}
	result := m.renderFrame(context.Background(), Source{Rect: rex.Rect{0, 0, .5, 1}})
	if result.err != nil || result.line != "fresh" {
		t.Fatalf("%+v", result)
	}
}

func TestPrepareFailureOrInvalidSizeDoesNotRender(t *testing.T) {
	for _, failure := range []error{nil, errors.New("unavailable")} {
		m := Manager{Prepare: func(context.Context, Source) (rex.Size, RenderFunc, error) {
			return rex.Size{}, func(context.Context, Source) (string, error) {
				t.Fatal("rendered without valid terminal context")
				return "", nil
			}, failure
		}}
		result := m.renderFrame(context.Background(), Source{})
		if !errors.Is(result.err, failure) || result.line != "" {
			t.Fatalf("%+v", result)
		}
	}
}

func TestHiddenSourceDoesNoPreparationWork(t *testing.T) {
	m := Manager{Prepare: func(context.Context, Source) (rex.Size, RenderFunc, error) {
		t.Fatal("prepared hidden source")
		return rex.Size{}, nil, nil
	}}
	if result := m.renderFrame(context.Background(), Source{Hidden: true}); result.err != nil {
		t.Fatal(result.err)
	}
}
