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

func TestConfirm_GermanVariants(t *testing.T) {
	for _, in := range []string{"j\n", "ja\n", "JA\n", "yes\n", "YES\n"} {
		p, _ := newStdin(in)
		ok, _ := p.Confirm("q?", false)
		if !ok {
			t.Errorf("input %q should map to true", in)
		}
	}
	for _, in := range []string{"n\n", "nein\n", "NEIN\n", "no\n"} {
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
	if strings.Count(out.String(), "Bitte 'y' oder 'n'") != 2 {
		t.Errorf("expected 2 re-ask messages, output:\n%s", out.String())
	}
}

func TestConfirm_ExhaustsAttemptsReturnsDefault(t *testing.T) {
	p, _ := newStdin("x\ny\nzzz\n") // 3rd input has 'y' but MaxAttempts=2 stops earlier
	p.MaxAttempts = 2
	ok, _ := p.Confirm("q?", true)
	if !ok {
		t.Error("expected fallback to default true")
	}
}

func TestConfirm_PromptIncludesDefaultSuffix(t *testing.T) {
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

func TestAlways_ReturnsFixedAnswer(t *testing.T) {
	if ok, _ := (Always{Answer: true}).Confirm("x?", false); !ok {
		t.Error("Always{true}.Confirm should return true")
	}
	if ok, _ := (Always{Answer: false}).Confirm("x?", true); ok {
		t.Error("Always{false}.Confirm should return false")
	}
}
