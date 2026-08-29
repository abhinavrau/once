package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestForm_FocusCycling(t *testing.T) {
	form := NewForm("Submit",
		FormItem{Key: "first", Label: "First", Field: NewTextField("first")},
		FormItem{Key: "second", Label: "Second", Field: NewTextField("second")},
	)
	assert.Equal(t, 0, form.Focused())

	formPressTab(&form)
	assert.Equal(t, 1, form.Focused())

	formPressTab(&form)
	assert.Equal(t, 2, form.Focused(), "submit button")

	formPressTab(&form)
	assert.Equal(t, 3, form.Focused(), "cancel button")

	formPressTab(&form)
	assert.Equal(t, 0, form.Focused(), "wraps to first field")
}

func TestForm_ShiftTabCycling(t *testing.T) {
	form := NewForm("Submit",
		FormItem{Key: "first", Label: "First", Field: NewTextField("first")},
		FormItem{Key: "second", Label: "Second", Field: NewTextField("second")},
	)

	formPressShiftTab(&form)
	assert.Equal(t, 3, form.Focused(), "cancel button")

	formPressShiftTab(&form)
	assert.Equal(t, 2, form.Focused(), "submit button")

	formPressShiftTab(&form)
	assert.Equal(t, 1, form.Focused())

	formPressShiftTab(&form)
	assert.Equal(t, 0, form.Focused())
}

func TestForm_EnterAdvancesFocus(t *testing.T) {
	form := NewForm("Submit",
		FormItem{Key: "first", Label: "First", Field: NewTextField("first")},
		FormItem{Key: "second", Label: "Second", Field: NewTextField("second")},
	)

	formPressEnter(&form)
	assert.Equal(t, 1, form.Focused())

	formPressEnter(&form)
	assert.Equal(t, 2, form.Focused(), "submit button")
}

func TestForm_SubmitAction(t *testing.T) {
	form := NewForm("Done",
		FormItem{Key: "field", Label: "Field", Field: NewTextField("val")},
	)
	submitted := false
	form.OnSubmit(func(f *Form) tea.Cmd {
		submitted = true
		return nil
	})

	formPressTab(&form)
	assert.Equal(t, 1, form.Focused(), "submit button")

	form, _ = form.Update(keyPressMsg("enter"))
	assert.True(t, submitted)
}

func TestForm_CancelAction(t *testing.T) {
	form := NewForm("Done",
		FormItem{Key: "field", Label: "Field", Field: NewTextField("val")},
	)
	cancelled := false
	form.OnCancel(func(f *Form) tea.Cmd {
		cancelled = true
		return nil
	})

	formPressTab(&form)
	formPressTab(&form)
	assert.Equal(t, 2, form.Focused(), "cancel button")

	form, _ = form.Update(keyPressMsg("enter"))
	assert.True(t, cancelled)
}

func TestForm_NoFields(t *testing.T) {
	form := NewForm("Done")
	assert.Equal(t, 0, form.Focused(), "submit button")

	formPressTab(&form)
	assert.Equal(t, 1, form.Focused(), "cancel button")

	formPressTab(&form)
	assert.Equal(t, 0, form.Focused(), "wraps to submit")
}

func TestForm_HideCancelButton(t *testing.T) {
	form := NewForm("Submit",
		FormItem{Key: "field", Label: "Field", Field: NewTextField("val")},
	)
	form.HideCancelButton()

	assert.NotContains(t, form.View(), "Cancel")

	formPressTab(&form)
	assert.Equal(t, 1, form.Focused(), "submit button")

	formPressTab(&form)
	assert.Equal(t, 0, form.Focused(), "wraps to field, no cancel stop")

	formPressShiftTab(&form)
	assert.Equal(t, 1, form.Focused(), "submit button")
}

func TestForm_HideCancelButton_EscCancels(t *testing.T) {
	form := NewForm("Submit",
		FormItem{Key: "field", Label: "Field", Field: NewTextField("val")},
	)
	form.HideCancelButton()

	cancelled := false
	form.OnCancel(func(f *Form) tea.Cmd {
		cancelled = true
		return nil
	})

	form, _ = form.Update(keyPressMsg("esc"))
	assert.True(t, cancelled)
}

func TestForm_EscLeftToFieldWithCancelButton(t *testing.T) {
	form := NewForm("Submit",
		FormItem{Key: "field", Label: "Field", Field: NewTextField("val")},
	)
	cancelled := false
	form.OnCancel(func(f *Form) tea.Cmd {
		cancelled = true
		return nil
	})

	form, _ = form.Update(keyPressMsg("esc"))
	assert.False(t, cancelled, "esc only cancels a form without a Cancel button")
}

func TestForm_AlignButtonsRight(t *testing.T) {
	buttonRow := func(form Form) (rowWidth, viewWidth int) {
		view := form.View()
		for _, line := range strings.Split(view, "\n") {
			if strings.Contains(line, "Submit") {
				return lipgloss.Width(strings.TrimRight(line, " ")), lipgloss.Width(view)
			}
		}
		t.Fatal("no button row found")
		return 0, 0
	}

	form := NewForm("Submit",
		FormItem{Key: "field", Label: "Field", Field: NewTextField("val")},
	)
	form, _ = form.Update(tea.WindowSizeMsg{Width: 60, Height: 24})

	rowWidth, viewWidth := buttonRow(form)
	assert.Less(t, rowWidth, viewWidth, "default row hugs the left edge")

	form.AlignButtonsRight()
	rowWidth, viewWidth = buttonRow(form)
	assert.Equal(t, viewWidth, rowWidth, "row ends flush with the right edge")
}

func TestTextField_DigitsOnly(t *testing.T) {
	field := NewTextField("number")
	field.SetDigitsOnly(true)
	field.Focus()

	field.Update(keyPressMsg("5"))
	assert.Equal(t, "5", field.Value())

	field.Update(keyPressMsg("a"))
	assert.Equal(t, "5", field.Value(), "non-digit rejected")

	field.Update(keyPressMsg("3"))
	assert.Equal(t, "53", field.Value())
}

func TestCheckboxField_Toggle(t *testing.T) {
	field := NewCheckboxField("Enable", false)
	assert.False(t, field.Checked())

	field.Update(keyPressMsg("space"))
	assert.True(t, field.Checked())

	field.Update(keyPressMsg("space"))
	assert.False(t, field.Checked())
}

func TestCheckboxField_Render(t *testing.T) {
	field := NewCheckboxField("TLS", true)
	assert.Equal(t, "[✓] TLS", field.View())

	field.Update(keyPressMsg("space"))
	assert.Equal(t, "[ ] TLS", field.View())
}

func TestCheckboxField_DisabledWhen(t *testing.T) {
	disabled := true
	field := NewCheckboxField("TLS", false)
	field.SetDisabledWhen(func() (bool, string) {
		return disabled, "Not available"
	})

	field.Update(keyPressMsg("space"))
	assert.False(t, field.Checked(), "toggle ignored when disabled")
	assert.Equal(t, "Not available", field.View())

	disabled = false
	field.Update(keyPressMsg("space"))
	assert.True(t, field.Checked(), "toggle works when enabled")
	assert.Equal(t, "[✓] TLS", field.View())
}

func TestRadioField_CycleAndArrows(t *testing.T) {
	field := NewRadioField("Local", "Tailnet", "Public")
	assert.Equal(t, 0, field.Selected())

	field.Update(keyPressMsg("right"))
	assert.Equal(t, 1, field.Selected())

	field.Update(keyPressMsg("space"))
	assert.Equal(t, 2, field.Selected())

	field.Update(keyPressMsg("right"))
	assert.Equal(t, 0, field.Selected(), "wraps forward")

	field.Update(keyPressMsg("left"))
	assert.Equal(t, 2, field.Selected(), "wraps backward")
}

func TestRadioField_Render(t *testing.T) {
	field := NewRadioField("Local", "Public")
	assert.Equal(t, "(•) Local   ( ) Public", field.View())

	field.Update(keyPressMsg("right"))
	assert.Equal(t, "( ) Local   (•) Public", field.View())
}

func TestRadioField_OnChange(t *testing.T) {
	field := NewRadioField("Local", "Tailnet")
	var got []int
	field.OnChange(func(selected int) { got = append(got, selected) })

	field.Update(keyPressMsg("right"))
	field.Update(keyPressMsg("right"))
	field.SetSelected(1)
	assert.Equal(t, 1, field.Selected())
	assert.Equal(t, []int{1, 0}, got, "SetSelected does not fire onChange")
}

func TestForm_ClickCyclesRadio(t *testing.T) {
	form := NewForm("Submit",
		FormItem{Key: "exposure", Label: "Exposure", Field: NewRadioField("Local", "Public")},
	)

	form = updateForm(form, MouseEvent{IsClick: true, Target: fieldTarget("exposure")})
	assert.Equal(t, 1, form.RadioField("exposure").Selected())
}

func TestForm_FieldValuesAccessible(t *testing.T) {
	form := NewForm("Submit",
		FormItem{Key: "name", Label: "Name", Field: NewTextField("name")},
	)

	formTypeText(&form, "hello")
	assert.Equal(t, "hello", form.TextField("name").Value())
}

func TestForm_ValidationBlocksSubmitWhenRequiredEmpty(t *testing.T) {
	form := NewForm("Submit",
		FormItem{Key: "name", Label: "Name", Field: NewTextField("name"), Required: true},
	)
	submitted := false
	form.OnSubmit(func(f *Form) tea.Cmd {
		submitted = true
		return nil
	})

	formFocusSubmit(&form)
	formPressEnter(&form)

	assert.False(t, submitted)
	assert.True(t, form.HasError())
	assert.Equal(t, "Name is required", form.Error())
}

func TestForm_ValidationAllowsSubmitWhenRequiredFilled(t *testing.T) {
	form := NewForm("Submit",
		FormItem{Key: "name", Label: "Name", Field: NewTextField("name"), Required: true},
	)
	submitted := false
	form.OnSubmit(func(f *Form) tea.Cmd {
		submitted = true
		return nil
	})

	formTypeText(&form, "hello")
	formFocusSubmit(&form)
	formPressEnter(&form)

	assert.True(t, submitted)
	assert.False(t, form.HasError())
}

func TestForm_ValidationTreatsWhitespaceAsEmpty(t *testing.T) {
	form := NewForm("Submit",
		FormItem{Key: "name", Label: "Name", Field: NewTextField("name"), Required: true},
	)
	submitted := false
	form.OnSubmit(func(f *Form) tea.Cmd {
		submitted = true
		return nil
	})

	formTypeText(&form, "   ")
	formFocusSubmit(&form)
	formPressEnter(&form)

	assert.False(t, submitted)
	assert.Equal(t, "Name is required", form.Error())
}

func TestForm_ValidationErrorClearsOnInput(t *testing.T) {
	form := NewForm("Submit",
		FormItem{Key: "name", Label: "Name", Field: NewTextField("name"), Required: true},
	)
	form.OnSubmit(func(f *Form) tea.Cmd { return nil })

	formFocusSubmit(&form)
	formPressEnter(&form)

	assert.True(t, form.HasError())
	assert.Equal(t, 0, form.Focused())

	formTypeText(&form, "x")

	assert.False(t, form.HasError())
}

func TestForm_ValidationNonRequiredDoesNotBlock(t *testing.T) {
	form := NewForm("Submit",
		FormItem{Key: "optional", Label: "Optional", Field: NewTextField("opt")},
		FormItem{Key: "required", Label: "Required", Field: NewTextField("req"), Required: true},
	)
	submitted := false
	form.OnSubmit(func(f *Form) tea.Cmd {
		submitted = true
		return nil
	})

	formPressTab(&form) // focus second field
	formTypeText(&form, "filled")
	formFocusSubmit(&form)
	formPressEnter(&form)

	assert.True(t, submitted)
	assert.False(t, form.HasError())
}

func TestForm_ValidationOnClickSubmit(t *testing.T) {
	form := NewForm("Submit",
		FormItem{Key: "name", Label: "Name", Field: NewTextField("name"), Required: true},
	)
	submitted := false
	form.OnSubmit(func(f *Form) tea.Cmd {
		submitted = true
		return nil
	})

	cmd := formClickSubmit(&form)

	assert.False(t, submitted)
	assert.True(t, form.HasError())

	rejected, ok := findFormRejection(cmd)
	require.True(t, ok, "expected a FormSubmitRejectedMsg")
	assert.Equal(t, FormSubmitRejectedMsg{Key: "name", Reason: "Name is required"}, rejected)
}

func TestForm_ValidationFocusesFirstError(t *testing.T) {
	form := NewForm("Submit",
		FormItem{Key: "first", Label: "First", Field: NewTextField("first")},
		FormItem{Key: "second", Label: "Second", Field: NewTextField("second"), Required: true},
		FormItem{Key: "third", Label: "Third", Field: NewTextField("third"), Required: true},
	)
	form.OnSubmit(func(f *Form) tea.Cmd { return nil })

	formFocusSubmit(&form)
	formPressEnter(&form)

	assert.Equal(t, 1, form.Focused(), "focused on first errored field")
	assert.Equal(t, "Second is required", form.Error())
}

func TestForm_ValidationFailureEmitsRejection(t *testing.T) {
	form := NewForm("Submit",
		FormItem{Key: "name", Label: "Name", Field: NewTextField("name"), Required: true},
	)
	form.OnSubmit(func(f *Form) tea.Cmd { return nil })

	formFocusSubmit(&form)
	cmd := formPressEnter(&form)

	rejected, ok := findFormRejection(cmd)
	require.True(t, ok, "expected a FormSubmitRejectedMsg")
	assert.Equal(t, FormSubmitRejectedMsg{Key: "name", Reason: "Name is required"}, rejected)
	assert.Equal(t, "Name is required", form.Error())
	assert.Equal(t, 0, form.Focused(), "refocuses the errored field")
}

func TestForm_CallbackErrorEmitsRejection(t *testing.T) {
	form := NewForm("Submit",
		FormItem{Key: "name", Label: "Name", Field: NewTextField("name")},
	)
	form.OnSubmit(func(f *Form) tea.Cmd {
		f.SetError("name", "that name is taken")
		return nil
	})

	formFocusSubmit(&form)
	cmd := formPressEnter(&form)

	rejected, ok := findFormRejection(cmd)
	require.True(t, ok, "expected a FormSubmitRejectedMsg")
	assert.Equal(t, FormSubmitRejectedMsg{Key: "name", Reason: "that name is taken"}, rejected)
	assert.Equal(t, "that name is taken", form.Error())
	assert.Equal(t, 0, form.Focused(), "refocuses the errored field")
}

func TestForm_CallbackErrorRefocusesSecondField(t *testing.T) {
	form := NewForm("Submit",
		FormItem{Key: "first", Label: "First", Field: NewTextField("first")},
		FormItem{Key: "second", Label: "Second", Field: NewTextField("second")},
	)
	form.OnSubmit(func(f *Form) tea.Cmd {
		f.SetError("second", "that value is taken")
		return nil
	})

	formFocusSubmit(&form)
	formPressEnter(&form)

	assert.Equal(t, 1, form.Focused(), "refocuses the errored field, not just the first")
}

func TestForm_CallbackErrorClearsOnInputAfterRefocus(t *testing.T) {
	form := NewForm("Submit",
		FormItem{Key: "name", Label: "Name", Field: NewTextField("name")},
	)
	form.OnSubmit(func(f *Form) tea.Cmd {
		f.SetError("name", "that name is taken")
		return nil
	})

	formFocusSubmit(&form)
	formPressEnter(&form)
	require.True(t, form.HasError())
	require.Equal(t, 0, form.Focused())

	formTypeText(&form, "x")

	assert.False(t, form.HasError(), "typing in the refocused field clears the inline error")
}

func TestForm_SuccessfulSubmitEmitsNoRejection(t *testing.T) {
	form := NewForm("Submit",
		FormItem{Key: "name", Label: "Name", Field: NewTextField("name"), Required: true},
	)
	form.OnSubmit(func(f *Form) tea.Cmd {
		return func() tea.Msg { return FormSubmitMsg{} }
	})

	formTypeText(&form, "hello")
	formFocusSubmit(&form)
	cmd := formPressEnter(&form)

	assert.False(t, form.HasError())
	_, ok := findFormRejection(cmd)
	assert.False(t, ok, "a successful submit emits no rejection")
}

func TestFormOnRebuild_CalledOnKeyPress(t *testing.T) {
	form := NewForm("Done",
		FormItem{Key: "field", Label: "Field", Field: NewTextField("val")},
	)
	called := false
	form.OnRebuild(func(f *Form) { called = true })

	formTypeText(&form, "x")
	assert.True(t, called)
}

func TestFormOnRebuild_NotCalledOnTab(t *testing.T) {
	form := NewForm("Done",
		FormItem{Key: "first", Label: "First", Field: NewTextField("first")},
		FormItem{Key: "second", Label: "Second", Field: NewTextField("second")},
	)
	called := false
	form.OnRebuild(func(f *Form) { called = true })

	formPressTab(&form)
	assert.False(t, called)
}

func TestFormOnRebuild_NotCalledOnWindowSize(t *testing.T) {
	form := NewForm("Done",
		FormItem{Key: "field", Label: "Field", Field: NewTextField("val")},
	)
	called := false
	form.OnRebuild(func(f *Form) { called = true })

	form, _ = form.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	assert.False(t, called)
}

func TestFormAppendItems(t *testing.T) {
	form := NewForm("Done",
		FormItem{Key: "first", Label: "First", Field: NewTextField("first")},
	)
	assert.Equal(t, 1, form.ItemCount())

	form.AppendItems(
		FormItem{Key: "second", Label: "Second", Field: NewTextField("second")},
		FormItem{Key: "third", Label: "Third", Field: NewTextField("third")},
	)
	assert.Equal(t, 3, form.ItemCount())
	assert.Equal(t, "", form.TextField("second").Value())
	assert.Equal(t, "", form.TextField("third").Value())
}

func TestFormAppendItems_FocusStability(t *testing.T) {
	form := NewForm("Done",
		FormItem{Key: "first", Label: "First", Field: NewTextField("first")},
		FormItem{Key: "second", Label: "Second", Field: NewTextField("second")},
	)
	formPressTab(&form)
	assert.Equal(t, 1, form.Focused())

	form.AppendItems(FormItem{Key: "third", Label: "Third", Field: NewTextField("third")})
	assert.Equal(t, 1, form.Focused(), "focus unchanged after append")
}

func TestForm_PanicsOnEmptyKey(t *testing.T) {
	assert.PanicsWithValue(t, `form item 1 ("Second") has an empty key`, func() {
		NewForm("Submit",
			FormItem{Key: "first", Label: "First", Field: NewTextField("first")},
			FormItem{Label: "Second", Field: NewTextField("second")},
		)
	})
}

func TestForm_PanicsOnDuplicateKey(t *testing.T) {
	assert.PanicsWithValue(t, `form item 1 ("Second") reuses key "name"`, func() {
		NewForm("Submit",
			FormItem{Key: "name", Label: "First", Field: NewTextField("first")},
			FormItem{Key: "name", Label: "Second", Field: NewTextField("second")},
		)
	})
}

func TestForm_PanicsOnUnknownKey(t *testing.T) {
	form := NewForm("Submit",
		FormItem{Key: "name", Label: "Name", Field: NewTextField("name")},
	)

	assert.PanicsWithValue(t, `form has no field with key "nickname"`, func() {
		form.TextField("nickname")
	})
}

func TestForm_SetErrorPanicsOnUnknownKey(t *testing.T) {
	form := NewForm("Submit",
		FormItem{Key: "name", Label: "Name", Field: NewTextField("name")},
	)

	assert.PanicsWithValue(t, `form has no field with key "nickname"`, func() {
		form.SetError("nickname", "boom")
	})
}

func TestForm_PanicsOnWrongFieldType(t *testing.T) {
	form := NewForm("Submit",
		FormItem{Key: "name", Label: "Name", Field: NewTextField("name")},
	)

	assert.PanicsWithValue(t, `form field "name" is not a CheckboxField`, func() {
		form.CheckboxField("name")
	})
	assert.PanicsWithValue(t, `form field "name" is not a RadioField`, func() {
		form.RadioField("name")
	})
}

func TestFormRemoveItem(t *testing.T) {
	form := NewForm("Done",
		FormItem{Key: "first", Label: "First", Field: NewTextField("first")},
		FormItem{Key: "second", Label: "Second", Field: NewTextField("second")},
	)

	form.RemoveItem("first")

	assert.Equal(t, 1, form.ItemCount())
	assert.Equal(t, "second", form.items[0].Key)
}

func TestFormRemoveItem_NoOpWhenAbsent(t *testing.T) {
	form := NewForm("Done",
		FormItem{Key: "first", Label: "First", Field: NewTextField("first")},
	)

	assert.NotPanics(t, func() { form.RemoveItem("missing") })
	assert.Equal(t, 1, form.ItemCount())
}

func TestFormOnRebuild_CalledOnRadioClick(t *testing.T) {
	form := NewForm("Submit",
		FormItem{Key: "exposure", Label: "Exposure", Field: NewRadioField("Local", "Public")},
	)
	called := false
	form.OnRebuild(func(f *Form) { called = true })

	form = updateForm(form, MouseEvent{IsClick: true, Target: fieldTarget("exposure")})

	assert.True(t, called, "a click that changes a field's value must rebuild, exactly like a keypress does")
}

func TestFormAppendItems_PanicsOnDuplicateKey(t *testing.T) {
	form := NewForm("Done",
		FormItem{Key: "first", Label: "First", Field: NewTextField("first")},
	)

	assert.PanicsWithValue(t, `form item 1 ("Again") reuses key "first"`, func() {
		form.AppendItems(FormItem{Key: "first", Label: "Again", Field: NewTextField("again")})
	})
}

// Helpers

func updateForm(f Form, msg tea.Msg) Form {
	f, _ = f.Update(msg)
	return f
}

func formPressTab(form *Form) {
	*form = updateForm(*form, keyPressMsg("tab"))
}

func formPressShiftTab(form *Form) {
	*form = updateForm(*form, keyPressMsg("shift+tab"))
}

func formPressEnter(form *Form) tea.Cmd {
	var cmd tea.Cmd
	*form, cmd = form.Update(keyPressMsg("enter"))
	return cmd
}

func formTypeText(form *Form, text string) {
	for _, r := range text {
		*form = updateForm(*form, keyPressMsg(string(r)))
	}
}

func formFocusSubmit(form *Form) {
	for form.Focused() != form.submitIndex() {
		formPressTab(form)
	}
}

func formClickSubmit(form *Form) tea.Cmd {
	var cmd tea.Cmd
	*form, cmd = form.Update(MouseEvent{IsClick: true, Target: "submit"})
	return cmd
}

// findFormRejection runs cmd, descending into batches, and returns the first
// rejection message it produces.
func findFormRejection(cmd tea.Cmd) (FormSubmitRejectedMsg, bool) {
	if cmd == nil {
		return FormSubmitRejectedMsg{}, false
	}

	switch msg := cmd().(type) {
	case FormSubmitRejectedMsg:
		return msg, true
	case tea.BatchMsg:
		for _, batched := range msg {
			if rejected, ok := findFormRejection(batched); ok {
				return rejected, true
			}
		}
	}

	return FormSubmitRejectedMsg{}, false
}

func TestFormSetCompactInlinesRadiosAndKeepsThemClickable(t *testing.T) {
	form := NewForm("Save",
		FormItem{Key: "name", Label: "Name", Field: NewTextField("")},
		FormItem{Key: "choice", Label: "Choice", Field: NewRadioField("A", "B")},
	)
	tall := lipgloss.Height(form.View())

	form.SetCompact(true)
	view := form.View()
	assert.Less(t, lipgloss.Height(view), tall)
	assert.Contains(t, ansi.Strip(view), "Choice  (•) A   ( ) B", "a compact radio renders label and options on one row")

	form, _ = form.Update(MouseEvent{IsClick: true, Target: "field:choice"})
	assert.Equal(t, 1, form.RadioField("choice").Selected(), "the inlined radio keeps its click target")
}

func TestFormSetMinHeightPadsBetweenFieldsAndButtons(t *testing.T) {
	form := NewForm("Save", FormItem{Key: "name", Label: "Name", Field: NewTextField("")})
	bare := lipgloss.Height(form.View())

	form.SetMinHeight(bare + 3)
	view := form.View()
	assert.Equal(t, bare+3, lipgloss.Height(view))

	lines := strings.Split(view, "\n")
	bottom := strings.Join(lines[len(lines)-3:], "\n")
	assert.Contains(t, bottom, "Save", "the button row stays last")
}
