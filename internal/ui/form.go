package ui

import (
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

// FormTheme dresses Huh's forms in the same roles as the rest of the binary:
// the accent for what has focus, success for what is picked, muted text for
// the explanations, faint for the frame.
func FormTheme(t Theme) huh.Theme {
	return huh.ThemeFunc(func(isDark bool) *huh.Styles {
		s := huh.ThemeBase(isDark)
		f := &s.Focused
		f.Base = f.Base.BorderForeground(t.Accent)
		f.Card = f.Base
		f.Title = f.Title.Foreground(t.Accent).Bold(true)
		f.NoteTitle = f.NoteTitle.Foreground(t.Accent).Bold(true).MarginBottom(1)
		f.Description = f.Description.Foreground(t.Muted)
		f.ErrorIndicator = f.ErrorIndicator.Foreground(t.Error)
		f.ErrorMessage = f.ErrorMessage.Foreground(t.Error)
		f.SelectSelector = f.SelectSelector.Foreground(t.Accent)
		f.NextIndicator = f.NextIndicator.Foreground(t.Accent)
		f.PrevIndicator = f.PrevIndicator.Foreground(t.Accent)
		f.Option = f.Option.Foreground(t.Text)
		f.MultiSelectSelector = f.MultiSelectSelector.Foreground(t.Accent)
		f.SelectedOption = f.SelectedOption.Foreground(t.Text)
		f.SelectedPrefix = lipgloss.NewStyle().Foreground(t.Success).SetString("✓ ")
		f.UnselectedOption = f.UnselectedOption.Foreground(t.Muted)
		f.UnselectedPrefix = lipgloss.NewStyle().Foreground(t.Faint).SetString("· ")
		f.FocusedButton = f.FocusedButton.Foreground(t.OnAccent).Background(t.Accent).Bold(true)
		f.Next = f.FocusedButton
		f.BlurredButton = f.BlurredButton.Foreground(t.Muted).Background(nil)
		f.TextInput.Cursor = f.TextInput.Cursor.Foreground(t.Accent)
		f.TextInput.Placeholder = f.TextInput.Placeholder.Foreground(t.Faint)
		f.TextInput.Prompt = f.TextInput.Prompt.Foreground(t.Accent)
		f.TextInput.Text = f.TextInput.Text.Foreground(t.Text)

		s.Blurred = s.Focused
		s.Blurred.Base = s.Focused.Base.BorderStyle(lipgloss.HiddenBorder())
		s.Blurred.Card = s.Blurred.Base
		s.Blurred.Title = s.Blurred.Title.Foreground(t.Muted).Bold(false)
		s.Blurred.NextIndicator = lipgloss.NewStyle()
		s.Blurred.PrevIndicator = lipgloss.NewStyle()

		s.Group.Title = s.Focused.Title
		s.Group.Description = s.Focused.Description
		s.Help.ShortKey = s.Help.ShortKey.Foreground(t.Muted)
		s.Help.ShortDesc = s.Help.ShortDesc.Foreground(t.Faint)
		s.Help.ShortSeparator = s.Help.ShortSeparator.Foreground(t.Faint)
		s.Help.FullKey = s.Help.FullKey.Foreground(t.Muted)
		s.Help.FullDesc = s.Help.FullDesc.Foreground(t.Faint)
		s.Help.FullSeparator = s.Help.FullSeparator.Foreground(t.Faint)
		return s
	})
}
