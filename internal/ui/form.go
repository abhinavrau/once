package ui

import (
	"fmt"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/basecamp/once/internal/mouse"
)

var formKeys = struct {
	Tab      key.Binding
	ShiftTab key.Binding
	Enter    key.Binding
}{
	Tab:      NewKeyBinding("tab"),
	ShiftTab: NewKeyBinding("shift+tab"),
	Enter:    NewKeyBinding("enter"),
}

type FormField interface {
	Update(tea.Msg) tea.Cmd
	View() string
	Focus() tea.Cmd
	Blur()
	SetWidth(int)
}

type valuer interface {
	Value() string
}

// TextField

type TextField struct {
	input      textinput.Model
	digitsOnly bool
}

func NewTextField(placeholder string) *TextField {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = placeholder
	input.CharLimit = 256
	return &TextField{input: input}
}

func (f *TextField) Value() string {
	return f.input.Value()
}

func (f *TextField) SetValue(v string) {
	f.input.SetValue(v)
}

func (f *TextField) SetPlaceholder(p string) {
	f.input.Placeholder = p
}

func (f *TextField) SetCharLimit(n int) {
	f.input.CharLimit = n
}

func (f *TextField) SetDigitsOnly(v bool) {
	f.digitsOnly = v
}

func (f *TextField) SetEchoPassword() {
	f.input.EchoMode = textinput.EchoPassword
}

func (f *TextField) Update(msg tea.Msg) tea.Cmd {
	if f.digitsOnly {
		if msg, ok := msg.(tea.KeyPressMsg); ok {
			if k := msg.Key(); k.Text != "" && !isDigitKey(k) {
				return nil
			}
		}
	}

	var cmd tea.Cmd
	f.input, cmd = f.input.Update(msg)
	return cmd
}

func (f *TextField) View() string {
	return f.input.View()
}

func (f *TextField) Focus() tea.Cmd {
	return f.input.Focus()
}

func (f *TextField) Blur() {
	f.input.Blur()
}

func (f *TextField) SetWidth(w int) {
	f.input.SetWidth(w)
}

// CheckboxField

type CheckboxField struct {
	label      string
	checked    bool
	disabledFn func() (disabled bool, text string)
}

func NewCheckboxField(label string, checked bool) *CheckboxField {
	return &CheckboxField{label: label, checked: checked}
}

func (f *CheckboxField) Checked() bool {
	return f.checked
}

func (f *CheckboxField) SetDisabledWhen(fn func() (disabled bool, text string)) {
	f.disabledFn = fn
}

func (f *CheckboxField) Toggle() {
	if f.disabledFn != nil {
		if disabled, _ := f.disabledFn(); disabled {
			return
		}
	}
	f.checked = !f.checked
}

func (f *CheckboxField) Update(msg tea.Msg) tea.Cmd {
	if msg, ok := msg.(tea.KeyPressMsg); ok {
		if msg.String() == "space" || msg.String() == " " {
			f.Toggle()
		}
	}
	return nil
}

func (f *CheckboxField) View() string {
	if f.disabledFn != nil {
		if disabled, text := f.disabledFn(); disabled {
			return text
		}
	}

	if f.checked {
		return "[✓] " + f.label
	}
	return "[ ] " + f.label
}

func (f *CheckboxField) Focus() tea.Cmd { return nil }
func (f *CheckboxField) Blur()          {}
func (f *CheckboxField) SetWidth(int)   {}

// RadioField

type RadioField struct {
	options  []string
	selected int
	onChange func(selected int)
}

func NewRadioField(options ...string) *RadioField {
	return &RadioField{options: options}
}

func (f *RadioField) Selected() int {
	return f.selected
}

func (f *RadioField) SetSelected(i int) {
	f.selected = i
}

func (f *RadioField) OnChange(fn func(selected int)) {
	f.onChange = fn
}

func (f *RadioField) Cycle() {
	f.choose((f.selected + 1) % len(f.options))
}

func (f *RadioField) Update(msg tea.Msg) tea.Cmd {
	if msg, ok := msg.(tea.KeyPressMsg); ok {
		switch msg.String() {
		case "left":
			f.choose((f.selected - 1 + len(f.options)) % len(f.options))
		case "right", "space", " ":
			f.Cycle()
		}
	}
	return nil
}

func (f *RadioField) View() string {
	parts := make([]string, len(f.options))
	for i, option := range f.options {
		if i == f.selected {
			parts[i] = "(•) " + option
		} else {
			parts[i] = "( ) " + option
		}
	}
	return strings.Join(parts, "   ")
}

func (f *RadioField) Focus() tea.Cmd { return nil }
func (f *RadioField) Blur()          {}
func (f *RadioField) SetWidth(int)   {}

// Private

func (f *RadioField) choose(i int) {
	if i == f.selected {
		return
	}
	f.selected = i
	if f.onChange != nil {
		f.onChange(i)
	}
}

// FormActionButton

type FormActionButton struct {
	Label   string
	OnPress func() tea.Msg
}

// Form

// FormItem declares one field of a form. Key addresses the field from the
// form's own file; it must be unique within the form and non-empty.
type FormItem struct {
	Key      string
	Label    string
	Field    FormField
	Required bool
}

type FormSubmitMsg struct{}
type FormCancelMsg struct{}
type FormActionMsg struct{ Msg tea.Msg }

// FormSubmitRejectedMsg reports a submit attempt that did not go through,
// naming the errored field and the reason the form renders inline. Nothing in
// production consumes it; it is here so observers can see a rejection.
type FormSubmitRejectedMsg struct {
	Key    string
	Reason string
}

type Form struct {
	items        []FormItem
	submitLabel  string
	actionButton *FormActionButton
	focused      int
	width        int
	errorKey     string
	error        string
	onSubmit     func(f *Form) tea.Cmd
	onCancel     func(f *Form) tea.Cmd
	onRebuild    func(f *Form)
}

func NewForm(submitLabel string, items ...FormItem) Form {
	requireUniqueKeys(nil, items)

	f := Form{
		items:       items,
		submitLabel: submitLabel,
	}

	if len(items) > 0 {
		items[0].Field.Focus()
	}

	return f
}

func (f Form) Init() tea.Cmd {
	if f.focused < len(f.items) {
		return f.items[f.focused].Field.Focus()
	}
	return nil
}

func (f Form) Update(msg tea.Msg) (Form, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		f.width = msg.Width
		inputWidth := min(f.width-4, 60)
		for _, item := range f.items {
			item.Field.SetWidth(inputWidth)
		}

	case MouseEvent:
		if msg.IsClick {
			return f.handleClick(msg.Target)
		}

	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, formKeys.Tab):
			return f.focusNext()
		case key.Matches(msg, formKeys.ShiftTab):
			return f.focusPrev()
		case key.Matches(msg, formKeys.Enter):
			return f.handleEnter()
		}
	}

	if f.focused < len(f.items) {
		_, isKey := msg.(tea.KeyPressMsg)
		_, isPaste := msg.(tea.PasteMsg)
		if isKey {
			f.clearErrorOnInput()
		}
		cmd := f.items[f.focused].Field.Update(msg)
		if (isKey || isPaste) && f.onRebuild != nil {
			f.onRebuild(&f)
			f.focused = min(f.focused, f.totalCount()-1)
		}
		return f, cmd
	}

	return f, nil
}

func (f Form) View() string {
	var parts []string

	errorStyle := lipgloss.NewStyle().Foreground(Colors.Error)

	for i, item := range f.items {
		label := Styles.Label.Render(item.Label)

		hasError := f.error != "" && item.Key == f.errorKey
		inputStyle := Styles.WithError(Styles.Focus(Styles.Input, f.focused == i), hasError)
		field := mouse.Mark(fieldTarget(item.Key), inputStyle.Render(item.Field.View()))

		if hasError {
			parts = append(parts, label, field, errorStyle.Render(f.error), "")
		} else {
			parts = append(parts, label, field, "")
		}
	}

	submitButton := mouse.Mark("submit", Styles.Focus(Styles.ButtonPrimary, f.focused == f.submitIndex()).
		Render(f.submitLabel))

	buttonParts := []string{submitButton}

	if f.actionButton != nil {
		actionBtn := mouse.Mark("action", Styles.Focus(Styles.Button, f.focused == f.actionIndex()).
			Render(f.actionButton.Label))
		buttonParts = append(buttonParts, actionBtn)
	}

	cancelButton := mouse.Mark("cancel", Styles.Focus(Styles.Button, f.focused == f.cancelIndex()).
		Render("Cancel"))

	buttonParts = append(buttonParts, cancelButton)
	buttons := lipgloss.JoinHorizontal(lipgloss.Center, buttonParts...)
	parts = append(parts, "", buttons)

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (f *Form) SetActionButton(label string, onPress func() tea.Msg) {
	f.actionButton = &FormActionButton{Label: label, OnPress: onPress}
}

func (f *Form) OnSubmit(fn func(f *Form) tea.Cmd) {
	f.onSubmit = fn
}

func (f *Form) OnCancel(fn func(f *Form) tea.Cmd) {
	f.onCancel = fn
}

func (f *Form) OnRebuild(fn func(f *Form)) {
	f.onRebuild = fn
}

func (f *Form) AppendItems(items ...FormItem) {
	requireUniqueKeys(f.items, items)

	inputWidth := min(f.width-4, 60)
	for _, item := range items {
		item.Field.SetWidth(inputWidth)
	}
	f.items = append(f.items, items...)
}

func (f Form) ItemCount() int {
	return len(f.items)
}

func (f Form) Focused() int {
	return f.focused
}

func (f Form) TextField(key string) *TextField {
	return fieldAs[*TextField](f, key, "TextField")
}

func (f Form) CheckboxField(key string) *CheckboxField {
	return fieldAs[*CheckboxField](f, key, "CheckboxField")
}

func (f Form) RadioField(key string) *RadioField {
	return fieldAs[*RadioField](f, key, "RadioField")
}

// SetError places message under the named field. The key must name a field of
// this form, so a drifted key dies rather than silently swallowing the error.
func (f *Form) SetError(key, message string) {
	_ = f.indexOf(key)
	f.errorKey = key
	f.error = message
}

func (f Form) HasError() bool {
	return f.error != ""
}

func (f Form) Error() string {
	return f.error
}

// Private

// indexOf returns the position of the keyed field, panicking if this form has
// no field with that key.
func (f Form) indexOf(key string) int {
	for i, item := range f.items {
		if item.Key == key {
			return i
		}
	}
	panic(fmt.Sprintf("form has no field with key %q", key))
}

func (f Form) focusNext() (Form, tea.Cmd) {
	f.blurCurrent()
	f.focused = (f.focused + 1) % f.totalCount()
	return f, f.focusCurrent()
}

func (f Form) focusPrev() (Form, tea.Cmd) {
	f.blurCurrent()
	f.focused = (f.focused - 1 + f.totalCount()) % f.totalCount()
	return f, f.focusCurrent()
}

func (f Form) blurCurrent() {
	if f.focused < len(f.items) {
		f.items[f.focused].Field.Blur()
	}
}

func (f Form) focusCurrent() tea.Cmd {
	if f.focused < len(f.items) {
		return f.items[f.focused].Field.Focus()
	}
	return nil
}

func (f Form) handleEnter() (Form, tea.Cmd) {
	switch {
	case f.focused < len(f.items):
		return f.focusNext()
	case f.actionButton != nil && f.focused == f.actionIndex():
		return f, func() tea.Msg { return f.actionButton.OnPress() }
	case f.focused == f.submitIndex():
		return f.submitIfValid()
	case f.focused == f.cancelIndex():
		if f.onCancel != nil {
			return f, f.onCancel(&f)
		}
		return f, nil
	}
	return f, nil
}

func (f Form) handleClick(target string) (Form, tea.Cmd) {
	if target == "" {
		return f, nil
	}

	for i, item := range f.items {
		if target == fieldTarget(item.Key) {
			switch field := item.Field.(type) {
			case *CheckboxField:
				field.Toggle()
			case *RadioField:
				field.Cycle()
			}
			return f.focusIndex(i)
		}
	}

	switch target {
	case "submit":
		f.blurCurrent()
		f.focused = f.submitIndex()
		return f.submitIfValid()
	case "action":
		if f.actionButton != nil {
			f.blurCurrent()
			f.focused = f.actionIndex()
			return f, func() tea.Msg { return f.actionButton.OnPress() }
		}
	case "cancel":
		f.blurCurrent()
		f.focused = f.cancelIndex()
		if f.onCancel != nil {
			return f, f.onCancel(&f)
		}
	}

	return f, nil
}

func (f *Form) clearErrorOnInput() {
	if f.error != "" && f.focused < len(f.items) && f.items[f.focused].Key == f.errorKey {
		f.error = ""
	}
}

func (f Form) submitIfValid() (Form, tea.Cmd) {
	var valid bool
	f, valid = f.validate()

	var cmd tea.Cmd
	if valid && f.onSubmit != nil {
		cmd = f.onSubmit(&f)
	}

	// validate cleared any earlier error, so an error still set here is one the
	// failing path just recorded (required-field validation or an OnSubmit
	// callback), and the submit did not go through. Either way, focus returns
	// to the errored field.
	if f.HasError() {
		var focusCmd tea.Cmd
		f, focusCmd = f.focusIndex(f.indexOf(f.errorKey))
		return f, tea.Batch(f.rejectionCmd(), cmd, focusCmd)
	}
	return f, cmd
}

func (f Form) rejectionCmd() tea.Cmd {
	rejected := FormSubmitRejectedMsg{Key: f.errorKey, Reason: f.error}
	return func() tea.Msg { return rejected }
}

func (f Form) validate() (Form, bool) {
	f.error = ""

	for _, item := range f.items {
		if !item.Required {
			continue
		}
		if v, ok := item.Field.(valuer); ok {
			if strings.TrimSpace(v.Value()) == "" {
				f.errorKey = item.Key
				f.error = item.Label + " is required"
				return f, false
			}
		}
	}

	return f, true
}

func (f Form) focusIndex(i int) (Form, tea.Cmd) {
	if i == f.focused {
		return f, nil
	}
	f.blurCurrent()
	f.focused = i
	return f, f.focusCurrent()
}

func (f Form) submitIndex() int { return len(f.items) }

func (f Form) actionIndex() int { return len(f.items) + 1 }

func (f Form) cancelIndex() int {
	if f.actionButton != nil {
		return len(f.items) + 2
	}
	return len(f.items) + 1
}

func (f Form) totalCount() int {
	return f.cancelIndex() + 1
}

// Helpers

// requireUniqueKeys panics unless every added item carries a key that is
// non-empty and unused, so a drifted form declaration dies in the unit tests.
func requireUniqueKeys(existing, added []FormItem) {
	seen := make(map[string]bool, len(existing)+len(added))
	for _, item := range existing {
		seen[item.Key] = true
	}

	for i, item := range added {
		index := len(existing) + i
		switch {
		case item.Key == "":
			panic(fmt.Sprintf("form item %d (%q) has an empty key", index, item.Label))
		case seen[item.Key]:
			panic(fmt.Sprintf("form item %d (%q) reuses key %q", index, item.Label, item.Key))
		}
		seen[item.Key] = true
	}
}

// fieldAs returns the keyed field as T, panicking if the form has no such key
// or the field is of another type. Both are programmer errors.
func fieldAs[T FormField](f Form, key, typeName string) T {
	field, ok := f.items[f.indexOf(key)].Field.(T)
	if !ok {
		panic(fmt.Sprintf("form field %q is not a %s", key, typeName))
	}
	return field
}

func fieldTarget(key string) string {
	return "field:" + key
}

func isDigitKey(k tea.Key) bool {
	return unicode.IsDigit(rune(k.Text[0]))
}
