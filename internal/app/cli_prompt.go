package app

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

func promptLocal(input, taskUUID, videoProfiles, audioRules, spriteSizes *string, subtitles, audio, video, sprites, encrypt *bool, stdout, stderr io.Writer) error {
	return promptLocalWithTerminal(bufio.NewReader(os.Stdin), os.Stdin, input, taskUUID, videoProfiles, audioRules, spriteSizes, subtitles, audio, video, sprites, encrypt, stdout, stderr)
}

func promptLocalWithReader(reader *bufio.Reader, input, taskUUID, videoProfiles, audioRules, spriteSizes *string, subtitles, audio, video, sprites, encrypt *bool, stdout, stderr io.Writer) error {
	return promptLocalWithTerminal(reader, nil, input, taskUUID, videoProfiles, audioRules, spriteSizes, subtitles, audio, video, sprites, encrypt, stdout, stderr)
}

func promptLocalWithTerminal(reader *bufio.Reader, terminalInput *os.File, input, taskUUID, videoProfiles, audioRules, spriteSizes *string, subtitles, audio, video, sprites, encrypt *bool, stdout, stderr io.Writer) error {
	fmt.Fprintln(stdout, "Forge Worker Local")
	if *input == "" {
		fmt.Fprintln(stdout, "\nInput")
		fmt.Fprint(stdout, "input path or URL: ")
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		*input = strings.TrimSpace(line)
	}
	if *taskUUID == "" {
		fmt.Fprintln(stdout, "\nTask")
		fmt.Fprint(stdout, "task uuid (blank to generate): ")
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		*taskUUID = strings.TrimSpace(line)
	}
	_ = stderr
	fmt.Fprintln(stdout, "\nVideo")
	*video = promptBool(reader, stdout, "enable video", *video)
	if *video {
		selected, err := promptVideoProfiles(reader, terminalInput, stdout, *videoProfiles)
		if err != nil {
			return err
		}
		*videoProfiles = selected
	}

	fmt.Fprintln(stdout, "\nAudio")
	*audio = promptBool(reader, stdout, "enable audio", *audio)
	if *audio {
		selected, err := promptAudioRules(reader, terminalInput, stdout, *audioRules)
		if err != nil {
			return err
		}
		*audioRules = selected
	}

	fmt.Fprintln(stdout, "\nSubtitles")
	*subtitles = promptBool(reader, stdout, "extract text subtitles", *subtitles)

	fmt.Fprintln(stdout, "\nSprites")
	*sprites = promptBool(reader, stdout, "enable thumbnail sprites", *sprites)
	if *sprites {
		selected, err := promptSpriteSizes(reader, terminalInput, stdout, *spriteSizes)
		if err != nil {
			return err
		}
		*spriteSizes = selected
	}
	if encrypt != nil && (*audio || *video) {
		fmt.Fprintln(stdout, "\nPackaging")
		*encrypt = promptBool(reader, stdout, "enable ClearKey encryption", *encrypt)
	}
	return nil
}

func promptBool(reader *bufio.Reader, output io.Writer, label string, fallback bool) bool {
	suffix := "n"
	if fallback {
		suffix = "y"
	}
	fmt.Fprintf(output, "%s [%s]: ", label, suffix)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fallback
	}
	line = strings.ToLower(strings.TrimSpace(line))
	if line == "" {
		return fallback
	}
	return line == "y" || line == "yes" || line == "true" || line == "1"
}

func promptText(reader *bufio.Reader, output io.Writer, label, fallback string) string {
	fmt.Fprintf(output, "%s [%s]: ", label, fallback)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fallback
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return fallback
	}
	return line
}
