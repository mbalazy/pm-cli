package service

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mbalazy/pm-cli/internal/storage"
)

func TestToggleFocus(t *testing.T) {
	store := newTestStore(t)
	ids := func() []string {
		fp, err := storage.ReadFocusPlan(store.RootDir())
		if err != nil {
			t.Fatal(err)
		}
		return fp.Tasks
	}

	t.Run("adds, then removes, and stamps today on a fresh plan", func(t *testing.T) {
		res, err := ToggleFocus(store, ToggleFocusInput{TaskID: "t-1"})
		if err != nil {
			t.Fatal(err)
		}
		if !res.Focused || res.Date != storage.Today() || len(res.TaskIDs) != 1 {
			t.Fatalf("res = %+v", res)
		}
		if got := ids(); len(got) != 1 || got[0] != "t-1" {
			t.Fatalf("plan = %v", got)
		}
		res, err = ToggleFocus(store, ToggleFocusInput{TaskID: "t-1"})
		if err != nil {
			t.Fatal(err)
		}
		if res.Focused || len(res.TaskIDs) != 0 || res.TaskIDs == nil {
			t.Fatalf("res = %+v", res)
		}
	})
	t.Run("a missing task is not found, an empty id a validation error", func(t *testing.T) {
		_, err := ToggleFocus(store, ToggleFocusInput{TaskID: "t-999"})
		if !errors.Is(err, storage.ErrTaskNotFound) {
			t.Fatalf("err = %v", err)
		}
		var ve *ValidationError
		if _, err := ToggleFocus(store, ToggleFocusInput{}); !errors.As(err, &ve) {
			t.Fatalf("err = %v", err)
		}
		if got := ids(); len(got) != 0 {
			t.Fatalf("a refused toggle must write nothing: %v", got)
		}
	})
	t.Run("a stale plan is carried over: cleaned and re-dated before the toggle", func(t *testing.T) {
		// t-1 is the fixture's only task: it survives the cleanup, gone-9
		// does not, and the toggle then REMOVES t-1 (it was on the plan).
		stale := "date: 2020-01-01\ntasks:\n  - t-1\n  - gone-9\n"
		if err := os.WriteFile(filepath.Join(store.RootDir(), "focus.yaml"), []byte(stale), 0o644); err != nil {
			t.Fatal(err)
		}
		res, err := ToggleFocus(store, ToggleFocusInput{TaskID: "t-1"})
		if err != nil {
			t.Fatal(err)
		}
		if res.Date != storage.Today() {
			t.Fatalf("date = %q", res.Date)
		}
		if res.Focused {
			t.Fatalf("t-1 was on the stale plan, the toggle must take it off: %+v", res)
		}
		if got := ids(); len(got) != 0 {
			t.Fatalf("plan = %v (gone-9 dropped by the cleanup, t-1 by the toggle)", got)
		}
	})
}
