package prompt

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// Prompter asks the user yes/no questions.
type Prompter interface {
	Confirm(question string, def bool) (bool, error)
}

// Stdin is the default Prompter using os.Stdin/os.Stdout.
type Stdin struct {
	In  io.Reader
	Out io.Writer
	// MaxAttempts caps the number of re-asks on invalid input. Zero falls back to 3.
	MaxAttempts int
}

// Default returns a Stdin prompter wired to the real stdio.
func Default() *Stdin {
	return &Stdin{In: os.Stdin, Out: os.Stdout, MaxAttempts: 3}
}

// Confirm prompts question with a y/N (or Y/n) suffix and returns the parsed answer.
// Empty input returns def. Invalid input re-asks up to MaxAttempts times before
// returning def with no error.
func (s *Stdin) Confirm(question string, def bool) (bool, error) {
	attempts := s.MaxAttempts
	if attempts <= 0 {
		attempts = 3
	}
	reader := bufio.NewReader(s.In)
	suffix := "[y/N]"
	if def {
		suffix = "[Y/n]"
	}
	for i := 0; i < attempts; i++ {
		if _, err := fmt.Fprintf(s.Out, "%s %s: ", question, suffix); err != nil {
			return def, err
		}
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return def, err
		}
		answer := strings.ToLower(strings.TrimSpace(line))
		switch answer {
		case "":
			return def, nil
		case "y", "yes", "j", "ja":
			return true, nil
		case "n", "no", "nein":
			return false, nil
		}
		if err == io.EOF {
			return def, nil
		}
		fmt.Fprintln(s.Out, "Bitte 'y' oder 'n' eingeben.")
	}
	return def, nil
}

// Always returns a fixed answer for every Confirm call. Useful for --no-prompt mode.
type Always struct {
	Answer bool
}

// Confirm returns the configured fixed answer.
func (a Always) Confirm(string, bool) (bool, error) {
	return a.Answer, nil
}
