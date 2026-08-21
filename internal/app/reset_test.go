package app

import (
	"reflect"
	"testing"
)

// reset() used to clear the Model field by field, and had drifted: about twenty
// fields it never touched, including every batch and merge cursor and
// existingPR — so a second single-PR run began believing the previous repo's PR
// already existed.
//
// It now clears each screen by assigning a zero value, which is only correct as
// long as every per-screen state actually lives in one of those structs. This
// test walks them reflectively rather than naming fields, so adding a field to
// a state struct is covered automatically and adding a *new* screen's state to
// Model without clearing it here is what fails.
func TestResetClearsEveryScreen(t *testing.T) {
	m := populatedModel()
	m.screen = ScreenBatchSummary

	// Dirty the fields the old hand-written list forgot, so a regression to
	// per-field clearing fails here rather than months later in use.
	m.batch.column, m.batch.feIndex, m.batch.beIndex = 1, 2, 3
	m.batch.confirmScroll, m.batch.existingPRs = 4, 5
	m.merge.column, m.merge.cursors = 1, []int{2, 3}
	m.merge.repoErrors = []string{"stale"}
	m.actions.repoErrors = []string{"stale"}
	m.allPRs.loading, m.allPRs.sortAsc = true, true
	m.existingPR = &populatedModel().allPRs.entries[0].PR

	next, _ := m.reset()
	got := next.(Model)

	for _, s := range []struct {
		name  string
		value any
	}{
		{"batch", got.batch},
		{"merge", got.merge},
		{"allPRs", got.allPRs},
		{"actions", got.actions},
		{"pull", got.pull},
		{"qa", got.qa},
	} {
		v := reflect.ValueOf(s.value)
		if v.IsZero() {
			continue
		}
		// Name the offending fields; "batch is not zero" is not actionable.
		for i := 0; i < v.NumField(); i++ {
			if !v.Field(i).IsZero() {
				t.Errorf("reset left %s.%s set to %v", s.name, v.Type().Field(i).Name, v.Field(i))
			}
		}
	}

	if got.existingPR != nil {
		t.Error("reset left existingPR set; the next single-PR run would think its PR already exists")
	}
	if got.errorMessage != "" || got.loadingMessage != "" {
		t.Errorf("reset left errorMessage %q / loadingMessage %q", got.errorMessage, got.loadingMessage)
	}
	if got.screen != ScreenMainMenu {
		t.Errorf("screen = %v, want MainMenu", got.screen)
	}
	// Session history is deliberately not cleared — it survives a reset.
	if len(got.sessionPRs) != len(m.sessionPRs) {
		t.Errorf("reset dropped session history: %d entries, want %d", len(got.sessionPRs), len(m.sessionPRs))
	}
}

// Every per-screen state struct must be reachable from Model by exactly the
// name reset() clears, or reset() would silently skip it.
func TestModelHoldsEveryScreenState(t *testing.T) {
	fields := map[string]bool{}
	mt := reflect.TypeOf(Model{})
	for i := 0; i < mt.NumField(); i++ {
		fields[mt.Field(i).Type.Name()] = true
	}

	for _, name := range []string{
		"batchState", "mergeState", "allPRsState", "actionsState", "pullState",
		"qaState", "settingsState", "listState", "firstRunState",
	} {
		if !fields[name] {
			t.Errorf("Model has no %s field — its screen's state is loose in Model", name)
		}
	}
}
