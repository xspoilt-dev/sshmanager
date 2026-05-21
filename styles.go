package main

import "github.com/charmbracelet/lipgloss"

// Color palette definitions (Catppuccin Mocha inspired)
var (
	colorBg      = lipgloss.Color("#1e1e2e")
	colorFg      = lipgloss.Color("#cdd6f4")
	colorSubtext = lipgloss.Color("#a6adc8")
	colorMuted   = lipgloss.Color("#585b70")
	colorPurple  = lipgloss.Color("#cba6f7")
	colorBlue    = lipgloss.Color("#89b4fa")
	colorGreen   = lipgloss.Color("#a6e3a1")
	colorYellow  = lipgloss.Color("#f9e2af")
	colorRed     = lipgloss.Color("#f38ba8")
	colorOverlay = lipgloss.Color("#313244")
)

// Lipgloss styles
var (
	styleTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPurple).
			MarginLeft(2).
			MarginBottom(1)

	styleSubtitle = lipgloss.NewStyle().
			Foreground(colorSubtext).
			Italic(true).
			MarginLeft(2).
			MarginBottom(1)

	styleServerCard = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorOverlay).
			Padding(0, 1).
			MarginBottom(1)

	styleServerCardSelected = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorPurple).
				Padding(0, 1).
				MarginBottom(1)

	styleServerAlias = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorBlue)

	styleServerDetails = lipgloss.NewStyle().
				Foreground(colorSubtext)

	styleProjectPath = lipgloss.NewStyle().
				Foreground(colorYellow).
				Italic(true)

	styleHelp = lipgloss.NewStyle().
			Foreground(colorMuted).
			MarginTop(1).
			MarginLeft(2)

	styleFormBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBlue).
			Padding(1, 2).
			MarginLeft(2).
			MarginTop(1)

	styleFormTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorBlue).
			MarginBottom(1)

	styleFormLabel = lipgloss.NewStyle().
			Foreground(colorSubtext).
			Width(15)

	styleFormInputActive = lipgloss.NewStyle().
				Foreground(colorPurple).
				Bold(true)

	styleFormInputMuted = lipgloss.NewStyle().
				Foreground(colorMuted)

	styleFilePanel = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Padding(0, 1)

	styleFilePanelActive = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorYellow).
				Padding(0, 1)

	styleFileDir = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorBlue)

	styleFileRegular = lipgloss.NewStyle().
				Foreground(colorFg)

	styleFileSelected = lipgloss.NewStyle().
				Foreground(colorPurple).
				Bold(true)

	styleIconDir = lipgloss.NewStyle().
			Foreground(colorYellow)

	styleIconFile = lipgloss.NewStyle().
			Foreground(colorBlue)

	styleStatusMsg = lipgloss.NewStyle().
			Foreground(colorGreen).
			Bold(true).
			Padding(0, 1)

	styleErrorMsg = lipgloss.NewStyle().
			Foreground(colorRed).
			Bold(true).
			Padding(0, 1)

	styleTerminalPanel = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Padding(0, 1)

	styleTerminalPanelActive = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorYellow).
				Padding(0, 1)

	styleModal = lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			BorderForeground(colorPurple).
			Padding(1, 3).
			Width(60).
			Align(lipgloss.Center)

	styleModalTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPurple).
			MarginBottom(1)

	styleModalProgressMsg = lipgloss.NewStyle().
			Foreground(colorSubtext).
			MarginBottom(1)

	styleModalHelp = lipgloss.NewStyle().
			Foreground(colorMuted).
			MarginTop(1)
)
