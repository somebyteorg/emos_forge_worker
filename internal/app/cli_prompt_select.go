package app

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
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
	for {
		value := promptText(reader, output, "selected video profiles", displayVideoProfiles(current))
		if err := validatePromptVideoProfiles(value); err == nil {
			return normalizeVideoProfilesCSV(value), nil
		} else {
			fmt.Fprintf(output, "invalid video profiles: %v\n", err)
		}
	}
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
	for {
		value := promptText(reader, output, "selected audio rules", displayAudioRules(current))
		if err := validatePromptAudioRules(value); err == nil {
			return normalizeAudioRulesCSV(value), nil
		} else {
			fmt.Fprintf(output, "invalid audio rules: %v\n", err)
		}
	}
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
	for {
		value := promptText(reader, output, "selected sprite sizes", current)
		normalized, err := normalizePromptSpriteSizes(value)
		if err == nil {
			return normalized, nil
		}
		fmt.Fprintf(output, "invalid sprite sizes: %v\n", err)
	}
}

func validatePromptVideoProfiles(value string) error {
	profiles := splitCSV(value)
	if len(profiles) == 0 {
		return errors.New("select at least one profile")
	}
	hasAuto := false
	for _, profile := range profiles {
		profile = normalizeVideoProfile(profile)
		switch profile {
		case "package", "720p", "1080p", "2160p":
		case "auto":
			hasAuto = true
		default:
			return fmt.Errorf("unknown profile %q", profile)
		}
	}
	if hasAuto && len(profiles) > 1 {
		return errors.New("auto cannot be combined with other profiles")
	}
	return nil
}

func validatePromptAudioRules(value string) error {
	rules := splitCSV(value)
	if len(rules) == 0 {
		return errors.New("select at least one rule")
	}
	hasNone := false
	for _, rule := range rules {
		rule = normalizeAudioRule(rule)
		switch rule {
		case "package", "aac":
		case "none":
			hasNone = true
		default:
			return fmt.Errorf("unknown rule %q", rule)
		}
	}
	if hasNone && len(rules) > 1 {
		return errors.New("none cannot be combined with other rules")
	}
	return nil
}

func normalizePromptSpriteSizes(value string) (string, error) {
	sizes := splitCSV(value)
	if len(sizes) == 0 {
		return "", errors.New("select at least one size")
	}
	normalized := make([]string, 0, len(sizes))
	for _, size := range sizes {
		left, right, ok := strings.Cut(strings.ToLower(size), "x")
		if !ok {
			return "", fmt.Errorf("size %q must use WIDTHxHEIGHT", size)
		}
		width, widthErr := strconv.Atoi(strings.TrimSpace(left))
		height, heightErr := strconv.Atoi(strings.TrimSpace(right))
		if widthErr != nil || heightErr != nil || width <= 0 || height <= 0 {
			return "", fmt.Errorf("size %q must contain positive dimensions", size)
		}
		normalized = append(normalized, fmt.Sprintf("%dx%d", width, height))
	}
	return strings.Join(normalized, ","), nil
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
