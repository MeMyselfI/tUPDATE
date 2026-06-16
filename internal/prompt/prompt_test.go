package prompt

import (
	"bytes"
	"strings"
	"testing"
)

func newStdin(input string) (*Stdin, *bytes.Buffer) {
	out := &bytes.Buffer{}
	return &Stdin{
		In:          strings.NewReader(input),
		Out:         out,
		MaxAttempts: 3,
	}, out
}

func TestConfirm_YesShort(t *testing.T) {
	p, _ := newStdin("y\n")
	ok, err := p.Confirm("question?", false)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("expected yes")
	}
}

func TestConfirm_NoShort(t *testing.T) {
	p, _ := newStdin("n\n")
	ok, err := p.Confirm("question?", true)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("expected no")
	}
}

func TestConfirm_EmptyUsesDefault(t *testing.T) {
	p, _ := newStdin("\n")
	ok, _ := p.Confirm("q?", true)
	if !ok {
		t.Error("expected default true")
	}
	p2, _ := newStdin("\n")
	ok2, _ := p2.Confirm("q?", false)
	if ok2 {
		t.Error("expected default false")
	}
}

func TestConfirm_AcceptsLocalizedYesVariants(t *testing.T) {
	for _, in := range []string{"y\n", "yes\n", "j\n", "ja\n", "JA\n", "o\n", "oui\n", "OUI\n"} {
		p, _ := newStdin(in)
		ok, _ := p.Confirm("q?", false)
		if !ok {
			t.Errorf("input %q should map to true", in)
		}
	}
}

func TestConfirm_AcceptsLocalizedNoVariants(t *testing.T) {
	for _, in := range []string{"n\n", "no\n", "nein\n", "NEIN\n", "non\n", "NON\n"} {
		p, _ := newStdin(in)
		ok, _ := p.Confirm("q?", true)
		if ok {
			t.Errorf("input %q should map to false", in)
		}
	}
}

func TestConfirm_ReAsksOnInvalid(t *testing.T) {
	p, out := newStdin("maybe\nfoo\ny\n")
	ok, err := p.Confirm("q?", false)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("expected final yes")
	}
	if !strings.Contains(out.String(), "Please enter 'y' or 'n'.") {
		t.Errorf("expected English retry message, output:\n%s", out.String())
	}
}

func TestConfirm_ExhaustsAttemptsReturnsDefault(t *testing.T) {
	p, _ := newStdin("x\nzzz\nqux\n")
	p.MaxAttempts = 2
	ok, _ := p.Confirm("q?", true)
	if !ok {
		t.Error("expected fallback to default true")
	}
}

func TestConfirm_DefaultSuffixesEnglish(t *testing.T) {
	p, out := newStdin("y\n")
	_, _ = p.Confirm("Backup?", false)
	if !strings.Contains(out.String(), "[y/N]") {
		t.Errorf("expected [y/N] suffix, got %q", out.String())
	}
	p2, out2 := newStdin("y\n")
	_, _ = p2.Confirm("Update?", true)
	if !strings.Contains(out2.String(), "[Y/n]") {
		t.Errorf("expected [Y/n] suffix, got %q", out2.String())
	}
}

func TestConfirm_LocalizedSuffixGerman(t *testing.T) {
	p := &Stdin{
		In:               strings.NewReader("y\n"),
		Out:              &bytes.Buffer{},
		SuffixNoDefault:  "[j/N]",
		SuffixYesDefault: "[J/n]",
		RetryMessage:     "Bitte 'j' oder 'n' eingeben.",
	}
	out := p.Out.(*bytes.Buffer)
	_, _ = p.Confirm("Backup?", false)
	if !strings.Contains(out.String(), "[j/N]") {
		t.Errorf("expected localized [j/N], got %q", out.String())
	}
}

func TestConfirm_LocalizedRetryFrench(t *testing.T) {
	p := &Stdin{
		In:           strings.NewReader("maybe\noui\n"),
		Out:          &bytes.Buffer{},
		MaxAttempts:  3,
		RetryMessage: "Veuillez saisir 'o' ou 'n'.",
	}
	out := p.Out.(*bytes.Buffer)
	ok, _ := p.Confirm("question ?", false)
	if !ok {
		t.Error("expected oui → true")
	}
	if !strings.Contains(out.String(), "Veuillez saisir 'o' ou 'n'.") {
		t.Errorf("expected French retry message: %q", out.String())
	}
}

func TestConfirmContinueOrShow_EmptyDefaultsToYes(t *testing.T) {
	p, _ := newStdin("\n")
	got, err := p.ConfirmContinueOrShow("q?")
	if err != nil {
		t.Fatal(err)
	}
	if got != AnswerYes {
		t.Errorf("empty input answer = %v, want AnswerYes", got)
	}
}

func TestConfirmContinueOrShow_YesNoShowAllInputs(t *testing.T) {
	cases := []struct {
		in   string
		want Answer
	}{
		{"y\n", AnswerYes},
		{"yes\n", AnswerYes},
		{"j\n", AnswerYes},
		{"ja\n", AnswerYes},
		{"o\n", AnswerYes},
		{"oui\n", AnswerYes},
		{"n\n", AnswerNo},
		{"no\n", AnswerNo},
		{"nein\n", AnswerNo},
		{"non\n", AnswerNo},
		{"a\n", AnswerShowAll},
		{"all\n", AnswerShowAll},
		{"alle\n", AnswerShowAll},
		{"ALL\n", AnswerShowAll},
		{"tout\n", AnswerShowAll},
		{"show\n", AnswerShowAll},
	}
	for _, c := range cases {
		p, _ := newStdin(c.in)
		got, err := p.ConfirmContinueOrShow("q?")
		if err != nil {
			t.Fatalf("input %q: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("input %q = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestConfirmContinueOrShow_DefaultSuffix(t *testing.T) {
	p, out := newStdin("y\n")
	_, _ = p.ConfirmContinueOrShow("q?")
	if !strings.Contains(out.String(), "[Y/n/a]") {
		t.Errorf("expected default suffix [Y/n/a], got %q", out.String())
	}
}

func TestConfirmContinueOrShow_LocalizedSuffix(t *testing.T) {
	p := &Stdin{
		In:                   strings.NewReader("a\n"),
		Out:                  &bytes.Buffer{},
		SuffixContinueOrShow: "[J/n/a]",
	}
	out := p.Out.(*bytes.Buffer)
	got, _ := p.ConfirmContinueOrShow("q?")
	if got != AnswerShowAll {
		t.Errorf("got %v, want AnswerShowAll", got)
	}
	if !strings.Contains(out.String(), "[J/n/a]") {
		t.Errorf("expected localized suffix [J/n/a], got %q", out.String())
	}
}

func TestConfirmContinueOrShow_ReAsksOnInvalid(t *testing.T) {
	p, out := newStdin("maybe\nfoo\na\n")
	got, _ := p.ConfirmContinueOrShow("q?")
	if got != AnswerShowAll {
		t.Errorf("got %v, want AnswerShowAll", got)
	}
	if strings.Count(out.String(), "Please enter 'y' or 'n'.") < 2 {
		t.Errorf("expected at least 2 retry messages, got: %s", out.String())
	}
}

func TestAlways_ContinueOrShowMapsToYesNo(t *testing.T) {
	if got, _ := (Always{Answer: true}).ConfirmContinueOrShow("?"); got != AnswerYes {
		t.Errorf("Always{true} ContinueOrShow = %v, want AnswerYes", got)
	}
	if got, _ := (Always{Answer: false}).ConfirmContinueOrShow("?"); got != AnswerNo {
		t.Errorf("Always{false} ContinueOrShow = %v, want AnswerNo", got)
	}
}

func TestAlways_ReturnsFixedAnswer(t *testing.T) {
	if ok, _ := (Always{Answer: true}).Confirm("x?", false); !ok {
		t.Error("Always{true}.Confirm should return true")
	}
	if ok, _ := (Always{Answer: false}).Confirm("x?", true); ok {
		t.Error("Always{false}.Confirm should return false")
	}
}
