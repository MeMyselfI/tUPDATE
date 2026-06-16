package i18n

import (
	"reflect"
	"strings"
	"testing"
)

func clearLocaleEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		t.Setenv(k, "")
	}
}

func TestLang_String(t *testing.T) {
	cases := map[Lang]string{
		LangEN: "en",
		LangDE: "de",
		LangFR: "fr",
	}
	for in, want := range cases {
		if got := in.String(); got != want {
			t.Errorf("Lang(%d).String() = %q, want %q", in, got, want)
		}
	}
}

func TestDetect_FallsBackToEnglishWhenEnvEmpty(t *testing.T) {
	clearLocaleEnv(t)
	if got := Detect(); got != LangEN {
		t.Errorf("Detect() with empty env = %v, want LangEN", got)
	}
}

func TestDetect_GermanFromLANG(t *testing.T) {
	clearLocaleEnv(t)
	t.Setenv("LANG", "de_DE.UTF-8")
	if got := Detect(); got != LangDE {
		t.Errorf("Detect() with LANG=de_DE.UTF-8 = %v, want LangDE", got)
	}
}

func TestDetect_FrenchFromLCAll(t *testing.T) {
	clearLocaleEnv(t)
	t.Setenv("LC_ALL", "fr_FR.UTF-8")
	if got := Detect(); got != LangFR {
		t.Errorf("Detect() with LC_ALL=fr_FR = %v, want LangFR", got)
	}
}

func TestDetect_LCMessagesWinsOverLANG(t *testing.T) {
	clearLocaleEnv(t)
	t.Setenv("LC_MESSAGES", "fr_FR")
	t.Setenv("LANG", "de_DE")
	if got := Detect(); got != LangFR {
		t.Errorf("Detect() with LC_MESSAGES=fr,LANG=de = %v, want LangFR", got)
	}
}

func TestDetect_LCAllWinsOverLCMessagesAndLANG(t *testing.T) {
	clearLocaleEnv(t)
	t.Setenv("LC_ALL", "de_AT.UTF-8")
	t.Setenv("LC_MESSAGES", "fr_FR")
	t.Setenv("LANG", "en_US")
	if got := Detect(); got != LangDE {
		t.Errorf("Detect() priority broken: got %v, want LangDE", got)
	}
}

func TestDetect_UnsupportedLocaleFallsBackToEnglish(t *testing.T) {
	for _, val := range []string{"C", "POSIX", "it_IT.UTF-8", "es_ES", "ja_JP"} {
		clearLocaleEnv(t)
		t.Setenv("LANG", val)
		if got := Detect(); got != LangEN {
			t.Errorf("Detect() with LANG=%s = %v, want LangEN (fallback)", val, got)
		}
	}
}

func TestDetect_ShortValueIgnored(t *testing.T) {
	clearLocaleEnv(t)
	t.Setenv("LANG", "d") // too short
	if got := Detect(); got != LangEN {
		t.Errorf("Detect() with LANG=d = %v, want LangEN", got)
	}
}

func TestDetect_CaseInsensitive(t *testing.T) {
	clearLocaleEnv(t)
	t.Setenv("LANG", "DE_DE.UTF-8")
	if got := Detect(); got != LangDE {
		t.Errorf("Detect() with LANG=DE_DE = %v, want LangDE", got)
	}
}

func TestGet_ReturnsEnglishForUnknown(t *testing.T) {
	got := Get(Lang(99))
	if got.NoChanges != en.NoChanges {
		t.Errorf("Get(unknown) did not return English fallback: %q", got.NoChanges)
	}
}

func TestGet_AllThreeBundlesPopulated(t *testing.T) {
	cases := map[Lang]Strings{
		LangEN: en,
		LangDE: de,
		LangFR: fr,
	}
	want := reflect.TypeOf(Strings{})
	for lang, bundle := range cases {
		v := reflect.ValueOf(bundle)
		for i := 0; i < want.NumField(); i++ {
			field := want.Field(i).Name
			val := v.Field(i).String()
			if strings.TrimSpace(val) == "" {
				t.Errorf("Get(%v).%s is empty", lang, field)
			}
		}
	}
}

func TestStrings_DistinctPerLanguage(t *testing.T) {
	// Pick a few characteristic strings and ensure each language is unique.
	cases := []struct {
		field string
		en    string
		de    string
		fr    string
	}{
		{"BackupQuestion", en.BackupQuestion, de.BackupQuestion, fr.BackupQuestion},
		{"UpdateQuestion", en.UpdateQuestion, de.UpdateQuestion, fr.UpdateQuestion},
		{"ContinueAnyway", en.ContinueAnyway, de.ContinueAnyway, fr.ContinueAnyway},
		{"NoChanges", en.NoChanges, de.NoChanges, fr.NoChanges},
		{"Done", en.Done, de.Done, fr.Done},
	}
	for _, c := range cases {
		if c.en == c.de || c.de == c.fr || c.en == c.fr {
			t.Errorf("%s strings are not distinct per language: en=%q de=%q fr=%q",
				c.field, c.en, c.de, c.fr)
		}
	}
}

func TestStrings_PromptSuffixesPerLanguage(t *testing.T) {
	if en.SuffixNoDefault != "[y/N]" {
		t.Errorf("EN suffix = %q", en.SuffixNoDefault)
	}
	if de.SuffixNoDefault != "[j/N]" {
		t.Errorf("DE suffix = %q", de.SuffixNoDefault)
	}
	if fr.SuffixNoDefault != "[o/N]" {
		t.Errorf("FR suffix = %q", fr.SuffixNoDefault)
	}
}
