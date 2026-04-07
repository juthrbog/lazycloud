package ui

import (
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

// HuhTheme returns a huh theme derived from the active LazyCloud theme.
func HuhTheme() huh.ThemeFunc {
	return func(bool) *huh.Styles {
		t := ActiveTheme
		s := huh.ThemeBase(true)

		// Focused field styles.
		s.Focused.Base = s.Focused.Base.BorderForeground(t.Secondary)
		s.Focused.Card = s.Focused.Base
		s.Focused.Title = s.Focused.Title.Foreground(t.Primary).Bold(true)
		s.Focused.NoteTitle = s.Focused.NoteTitle.Foreground(t.Primary).Bold(true).MarginBottom(1)
		s.Focused.Description = s.Focused.Description.Foreground(t.Muted)
		s.Focused.ErrorIndicator = s.Focused.ErrorIndicator.Foreground(t.Error)
		s.Focused.ErrorMessage = s.Focused.ErrorMessage.Foreground(t.Error)
		s.Focused.SelectSelector = s.Focused.SelectSelector.Foreground(t.Accent)
		s.Focused.NextIndicator = s.Focused.NextIndicator.Foreground(t.Accent)
		s.Focused.PrevIndicator = s.Focused.PrevIndicator.Foreground(t.Accent)
		s.Focused.Option = s.Focused.Option.Foreground(t.Text)
		s.Focused.MultiSelectSelector = s.Focused.MultiSelectSelector.Foreground(t.Accent)
		s.Focused.SelectedOption = s.Focused.SelectedOption.Foreground(t.Accent)
		s.Focused.SelectedPrefix = lipgloss.NewStyle().Foreground(t.Accent).SetString("✓ ")
		s.Focused.UnselectedPrefix = lipgloss.NewStyle().Foreground(t.Muted).SetString("• ")
		s.Focused.UnselectedOption = s.Focused.UnselectedOption.Foreground(t.Text)
		s.Focused.FocusedButton = s.Focused.FocusedButton.Foreground(t.BrightText).Background(t.Accent)
		s.Focused.Next = s.Focused.FocusedButton
		s.Focused.BlurredButton = s.Focused.BlurredButton.Foreground(t.Text).Background(t.Overlay)
		s.Focused.TextInput.Cursor = s.Focused.TextInput.Cursor.Foreground(t.Accent)
		s.Focused.TextInput.Placeholder = s.Focused.TextInput.Placeholder.Foreground(t.Muted)
		s.Focused.TextInput.Prompt = s.Focused.TextInput.Prompt.Foreground(t.Accent)
		s.Focused.TextInput.Text = s.Focused.TextInput.Text.Foreground(t.Text)

		// Blurred = focused with hidden border.
		s.Blurred = s.Focused
		s.Blurred.Base = s.Focused.Base.BorderStyle(lipgloss.HiddenBorder())
		s.Blurred.Card = s.Blurred.Base
		s.Blurred.NextIndicator = lipgloss.NewStyle()
		s.Blurred.PrevIndicator = lipgloss.NewStyle()
		s.Blurred.TextInput.Text = s.Blurred.TextInput.Text.Foreground(t.SubText)

		// Group styles.
		s.Group.Title = s.Focused.Title
		s.Group.Description = s.Focused.Description

		return s
	}
}
