package prompt

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// Answer enumerates the possible outcomes of a three-way prompt.
type Answer int

const (
	// AnswerNo: user wants to abort.
	AnswerNo Answer = iota
	// AnswerYes: user wants to continue.
	AnswerYes
	// AnswerShowAll: user wants to see the full list before deciding.
	AnswerShowAll
)

// Prompter asks the user yes/no questions and three-way (yes/no/show) questions.
type Prompter interface {
	Confirm(question string, def bool) (bool, error)
	ConfirmContinueOrShow(question string) (Answer, error)
}

// Stdin is the default Prompter using stdio.
//
// Accepted yes inputs: "y", "yes", "j", "ja", "o", "oui" (case-insensitive).
// Accepted no inputs: "n", "no", "nein", "non" (case-insensitive).
// Empty input returns the default. Invalid input re-asks up to MaxAttempts
// times before giving up and returning the default.
//
// Suffix and retry message default to English ("[Y/n]" / "[y/N]" / "Please
// enter 'y' or 'n'."). Callers may override via the matching fields to render
// the prompt in the user's locale.
type Stdin struct {
	In  io.Reader
	Out io.Writer
	// MaxAttempts caps the number of re-asks on invalid input. Zero falls back to 3.
	MaxAttempts int
	// SuffixYesDefault is appended after the question when the default is true.
	// Empty falls back to "[Y/n]".
	SuffixYesDefault string
	// SuffixNoDefault is appended after the question when the default is false.
	// Empty falls back to "[y/N]".
	SuffixNoDefault string
	// SuffixContinueOrShow is used for the three-way prompt. Empty falls back
	// to "[Y/n/a]".
	SuffixContinueOrShow string
	// RetryMessage is printed after the user enters an unrecognised value.
	// Empty falls back to "Please enter 'y' or 'n'.".
	RetryMessage string

	// reader caches the buffered reader across Confirm calls so that data
	// past the first newline (which bufio normally pre-fetches) is not lost
	// between successive prompts.
	reader *bufio.Reader
}

// Default returns a Stdin prompter wired to the real stdio with English defaults.
func Default() *Stdin {
	return &Stdin{In: os.Stdin, Out: os.Stdout, MaxAttempts: 3}
}

var (
	yesWords     = []string{"y", "yes", "j", "ja", "o", "oui"}
	noWords      = []string{"n", "no", "nein", "non"}
	showAllWords = []string{"a", "all", "alle", "tout", "show"}
)

// Confirm prompts the question with a yes/no suffix and returns the parsed answer.
func (s *Stdin) Confirm(question string, def bool) (bool, error) {
	attempts := s.MaxAttempts
	if attempts <= 0 {
		attempts = 3
	}
	suffix := s.suffix(def)
	retry := s.retryMessage()
	if s.reader == nil {
		s.reader = bufio.NewReader(s.In)
	}
	reader := s.reader

	for i := 0; i < attempts; i++ {
		if _, err := fmt.Fprintf(s.Out, "%s %s: ", question, suffix); err != nil {
			return def, err
		}
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return def, err
		}
		answer := strings.ToLower(strings.TrimSpace(line))
		if answer == "" {
			return def, nil
		}
		if matches(answer, yesWords) {
			return true, nil
		}
		if matches(answer, noWords) {
			return false, nil
		}
		if err == io.EOF {
			return def, nil
		}
		fmt.Fprintln(s.Out, retry)
	}
	return def, nil
}

// ConfirmContinueOrShow asks a three-way question with default = AnswerYes.
// Accepted answers (case-insensitive):
//   - "" / "y" / "yes" / "j" / "ja" / "o" / "oui"            → AnswerYes
//   - "n" / "no" / "nein" / "non"                              → AnswerNo
//   - "a" / "all" / "alle" / "tout" / "show"                  → AnswerShowAll
//
// Invalid input is re-asked up to MaxAttempts times before falling back to
// AnswerYes (the default).
func (s *Stdin) ConfirmContinueOrShow(question string) (Answer, error) {
	attempts := s.MaxAttempts
	if attempts <= 0 {
		attempts = 3
	}
	suffix := s.suffixContinueOrShow()
	retry := s.retryMessage()
	if s.reader == nil {
		s.reader = bufio.NewReader(s.In)
	}
	reader := s.reader

	for i := 0; i < attempts; i++ {
		if _, err := fmt.Fprintf(s.Out, "%s %s: ", question, suffix); err != nil {
			return AnswerYes, err
		}
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return AnswerYes, err
		}
		answer := strings.ToLower(strings.TrimSpace(line))
		if answer == "" {
			return AnswerYes, nil
		}
		if matches(answer, yesWords) {
			return AnswerYes, nil
		}
		if matches(answer, noWords) {
			return AnswerNo, nil
		}
		if matches(answer, showAllWords) {
			return AnswerShowAll, nil
		}
		if err == io.EOF {
			return AnswerYes, nil
		}
		fmt.Fprintln(s.Out, retry)
	}
	return AnswerYes, nil
}

func (s *Stdin) suffixContinueOrShow() string {
	if s.SuffixContinueOrShow != "" {
		return s.SuffixContinueOrShow
	}
	return "[Y/n/a]"
}

func (s *Stdin) suffix(def bool) string {
	if def {
		if s.SuffixYesDefault != "" {
			return s.SuffixYesDefault
		}
		return "[Y/n]"
	}
	if s.SuffixNoDefault != "" {
		return s.SuffixNoDefault
	}
	return "[y/N]"
}

func (s *Stdin) retryMessage() string {
	if s.RetryMessage != "" {
		return s.RetryMessage
	}
	return "Please enter 'y' or 'n'."
}

func matches(input string, words []string) bool {
	for _, w := range words {
		if input == w {
			return true
		}
	}
	return false
}

// Always returns a fixed answer for every Confirm call. Useful for --no-prompt mode.
type Always struct {
	Answer bool
}

// Confirm returns the configured fixed answer.
func (a Always) Confirm(string, bool) (bool, error) {
	return a.Answer, nil
}

// ConfirmContinueOrShow returns AnswerYes if Answer is true, otherwise AnswerNo.
// Automation never asks for the details listing.
func (a Always) ConfirmContinueOrShow(string) (Answer, error) {
	if a.Answer {
		return AnswerYes, nil
	}
	return AnswerNo, nil
}
