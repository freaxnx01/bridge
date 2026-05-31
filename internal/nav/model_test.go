package nav

import "testing"

func TestInitialModel_StartsOnPickerFocusFilter(t *testing.T) {
	m := initialModel(Config{ReposRoots: []string{"/tmp"}})
	if m.screen != screenPicker {
		t.Errorf("screen = %d, want screenPicker", m.screen)
	}
	if m.pickerFocus != focusFilter {
		t.Errorf("pickerFocus = %d, want focusFilter", m.pickerFocus)
	}
	if m.remoteState != loadPending {
		t.Errorf("remoteState = %d, want loadPending", m.remoteState)
	}
}
