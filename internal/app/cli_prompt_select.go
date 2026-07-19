package app

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

type promptChoice struct {
	Label    string
	Value    string
	Selected bool
}

func promptVideoProfiles(reader *bufio.Reader, terminalInput *os.File, output io.Writer, current string) (string, error) {
	choices := videoProfileChoices(current)
	if interactiveSelectAvailable(terminalInput, output) {
		values, err := promptTerminalMultiSelect(reader, terminalInput, output, "selected video profiles", choices)
		if err != nil {
			return "", err
		}
		return normalizeVideoProfilesCSV(strings.Join(values, ",")), nil
	}
	fmt.Fprintln(output, "video profile options: package, 720p, 1080p")
	return normalizeVideoProfilesCSV(promptText(reader, output, "selected video profiles", displayVideoProfiles(current))), nil
}

func promptAudioRules(reader *bufio.Reader, terminalInput *os.File, output io.Writer, current string) (string, error) {
	choices := audioRuleChoices(current)
	if interactiveSelectAvailable(terminalInput, output) {
		values, err := promptTerminalMultiSelect(reader, terminalInput, output, "selected audio rules", choices)
		if err != nil {
			return "", err
		}
		return normalizeAudioRulesCSV(strings.Join(values, ",")), nil
	}
	fmt.Fprintln(output, "audio rule options: package, aac")
	return normalizeAudioRulesCSV(promptText(reader, output, "selected audio rules", displayAudioRules(current))), nil
}

func promptSpriteSizes(reader *bufio.Reader, terminalInput *os.File, output io.Writer, current string) (string, error) {
	choices := spriteSizeChoices(current)
	if interactiveSelectAvailable(terminalInput, output) {
		values, err := promptTerminalMultiSelect(reader, terminalInput, output, "selected sprite sizes", choices)
		if err != nil {
			return "", err
		}
		if len(values) == 0 {
			return current, nil
		}
		return strings.Join(values, ","), nil
	}
	fmt.Fprintf(output, "sprite size options: %s\n", current)
	return promptText(reader, output, "selected sprite sizes", current), nil
}

func videoProfileChoices(current string) []promptChoice {
	selected := selectedSet(splitVideoProfiles(current))
	return []promptChoice{
		{Label: "package", Value: "package", Selected: selected["package"]},
		{Label: "720p", Value: "720p", Selected: selected["720p"]},
		{Label: "1080p", Value: "1080p", Selected: selected["1080p"]},
	}
}

func audioRuleChoices(current string) []promptChoice {
	selected := selectedSet(splitAudioRules(current))
	return []promptChoice{
		{Label: "package", Value: "package", Selected: selected["package"]},
		{Label: "aac", Value: "aac", Selected: selected["aac"]},
	}
}

func spriteSizeChoices(current string) []promptChoice {
	sizes := splitCSV(current)
	choices := make([]promptChoice, 0, len(sizes))
	for _, size := range sizes {
		choices = append(choices, promptChoice{Label: size, Value: size, Selected: true})
	}
	return choices
}

func selectedSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func interactiveSelectAvailable(input *os.File, output io.Writer) bool {
	if input == nil || !term.IsTerminal(int(input.Fd())) {
		return false
	}
	file, ok := output.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}

func promptTerminalMultiSelect(reader *bufio.Reader, input *os.File, output io.Writer, label string, choices []promptChoice) ([]string, error) {
	state, err := term.MakeRaw(int(input.Fd()))
	if err != nil {
		return nil, err
	}
	defer term.Restore(int(input.Fd()), state)
	return promptKeyMultiSelect(reader, output, label, choices)
}

func promptKeyMultiSelect(reader *bufio.Reader, output io.Writer, label string, choices []promptChoice) ([]string, error) {
	if len(choices) == 0 {
		return nil, nil
	}
	cursor := firstSelectedChoice(choices)
	renderedLines := 0
	for {
		if renderedLines > 0 {
			fmt.Fprintf(output, "\x1b[%dA\x1b[J", renderedLines)
		}
		renderedLines = renderMultiSelect(output, label, choices, cursor)
		key, err := readPromptKey(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return selectedChoiceValues(choices), nil
			}
			return nil, err
		}
		switch key {
		case "up":
			cursor = (cursor + len(choices) - 1) % len(choices)
		case "down":
			cursor = (cursor + 1) % len(choices)
		case "space":
			choices[cursor].Selected = !choices[cursor].Selected
		case "enter":
			values := selectedChoiceValues(choices)
			if len(values) == 0 {
				fmt.Fprint(output, "\a")
				continue
			}
			fmt.Fprint(output, "\r\n")
			return values, nil
		case "interrupt":
			return nil, errors.New("prompt interrupted")
		}
	}
}

func renderMultiSelect(output io.Writer, label string, choices []promptChoice, cursor int) int {
	fmt.Fprintf(output, "%s\r\n", label)
	fmt.Fprint(output, "Use up/down or j/k to move, space to toggle, enter to confirm.\r\n")
	for i, choice := range choices {
		pointer := " "
		if i == cursor {
			pointer = ">"
		}
		check := " "
		if choice.Selected {
			check = "x"
		}
		fmt.Fprintf(output, "%s [%s] %s\r\n", pointer, check, choice.Label)
	}
	return len(choices) + 2
}

func readPromptKey(reader *bufio.Reader) (string, error) {
	value, err := reader.ReadByte()
	if err != nil {
		return "", err
	}
	switch value {
	case '\r', '\n':
		return "enter", nil
	case ' ':
		return "space", nil
	case 'j', 'J', '\t':
		return "down", nil
	case 'k', 'K':
		return "up", nil
	case 3:
		return "interrupt", nil
	case 0x1b:
		return readEscapeKey(reader)
	default:
		return "", nil
	}
}

func readEscapeKey(reader *bufio.Reader) (string, error) {
	value, err := reader.ReadByte()
	if err != nil {
		return "", err
	}
	if value != '[' {
		return "", nil
	}
	value, err = reader.ReadByte()
	if err != nil {
		return "", err
	}
	switch value {
	case 'A':
		return "up", nil
	case 'B':
		return "down", nil
	default:
		return "", nil
	}
}

func firstSelectedChoice(choices []promptChoice) int {
	for i, choice := range choices {
		if choice.Selected {
			return i
		}
	}
	return 0
}

func selectedChoiceValues(choices []promptChoice) []string {
	values := make([]string, 0, len(choices))
	for _, choice := range choices {
		if choice.Selected {
			values = append(values, choice.Value)
		}
	}
	return values
}
