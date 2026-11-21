package main

import "testing"

// Every language has every text, with the same number of inserted values.
func TestLogTexts(t *testing.T) {
	en := readLogTexts("en.txt")
	for lang, texts := range logTexts {
		for k, src := range en {
			to, ok := texts[k]
			if !ok {
				t.Errorf("%s: %s is missing", lang, k)
				continue
			}
			if a, b := len(verbRe.FindAllString(src, -1)), len(verbRe.FindAllString(to, -1)); a != b {
				t.Errorf("%s %s: %d values in English, %d in the translation", lang, k, a, b)
			}
		}
	}
	if got := translateLog("Event 12 (door person) protected by bob", "da"); got != "Hændelse 12 (door person) beskyttet af bob" {
		t.Errorf("da: %q", got)
	}
	if got := translateLog("door/main: running (2560x1920)", "fr"); got != "door/main : en marche (2560x1920)" {
		t.Errorf("fr: %q", got)
	}
	ll := LogLine{}
	splitLine(&ll, translateLog("door/main: running (2560x1920)", "fr"), "fr")
	if ll.Obj != "door" || ll.Msg != "Main: en marche (2560x1920)" {
		t.Errorf("split: %+v", ll)
	}
	if got := translateLog("an unknown line", "de"); got != "an unknown line" {
		t.Errorf("unknown: %q", got)
	}
}
