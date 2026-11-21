package main

// The log in the chosen language. The code writes the log in English and it is stored in English; each line is
// translated when it is shown (logstore.go). Every English format string in logtext/en.txt becomes a pattern, and the
// inserted values (%s, %d …) are put into the translation in the same order. Unknown lines are kept as they are.

import (
	"bufio"
	"embed"
	"regexp"
	"sort"
	"strings"
)

//go:embed logtext/*.txt
var logTextFS embed.FS

type logPattern struct {
	re  *regexp.Regexp
	key string
	n   int // number of fixed characters – the most specific patterns are tried first
}

var (
	logPatterns []logPattern
	logTexts    = map[string]map[string]string{} // language → number → text
	logValueKey = map[string]string{}            // English value → number (v1, v2 …)
	verbRe      = regexp.MustCompile(`%(\.\d+)?[sdvqfw]|%%`)
)

func readLogTexts(name string) map[string]string {
	out := map[string]string{}
	f, err := logTextFS.Open("logtext/" + name)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := sc.Text()
		if strings.HasPrefix(l, "#") {
			continue
		}
		if k, v, ok := strings.Cut(l, "|"); ok {
			out[k] = v
		}
	}
	return out
}

func init() {
	entries, _ := logTextFS.ReadDir("logtext")
	for _, e := range entries {
		if lang, ok := strings.CutSuffix(e.Name(), ".txt"); ok && lang != "en" {
			logTexts[lang] = readLogTexts(e.Name())
		}
	}
	for key, f := range readLogTexts("en.txt") {
		if strings.HasPrefix(key, "v") {
			logValueKey[f] = key
			continue
		}
		var b strings.Builder
		b.WriteString("^")
		rest, fixed := f, 0
		for {
			loc := verbRe.FindStringIndex(rest)
			if loc == nil {
				b.WriteString(regexp.QuoteMeta(rest))
				fixed += len(rest)
				break
			}
			b.WriteString(regexp.QuoteMeta(rest[:loc[0]]))
			fixed += loc[0]
			if rest[loc[0]:loc[1]] == "%%" {
				b.WriteString("%")
			} else {
				b.WriteString("(.*?)")
			}
			rest = rest[loc[1]:]
		}
		b.WriteString("$")
		logPatterns = append(logPatterns, logPattern{regexp.MustCompile(b.String()), key, fixed})
	}
	sort.SliceStable(logPatterns, func(i, j int) bool { return logPatterns[i].n > logPatterns[j].n })
}

// translateLog returns an English log line in the language lang (unchanged if the line or language is not known).
func translateLog(line, lang string) string {
	texts := logTexts[lang]
	if texts == nil {
		return line
	}
	for _, m := range logPatterns {
		g := m.re.FindStringSubmatch(line)
		if g == nil {
			continue
		}
		to, ok := texts[m.key]
		if !ok {
			return line
		}
		var out strings.Builder
		i := 1
		for {
			loc := verbRe.FindStringIndex(to)
			if loc == nil {
				out.WriteString(to)
				break
			}
			out.WriteString(to[:loc[0]])
			if to[loc[0]:loc[1]] == "%%" {
				out.WriteString("%")
			} else if i < len(g) {
				out.WriteString(translateValue(g[i], lang))
				i++
			}
			to = to[loc[1]:]
		}
		return out.String()
	}
	return line
}

// translateValue: inserted values that are words themselves (e.g. "protected", "home", camera errors).
func translateValue(s, lang string) string {
	if k, ok := logValueKey[s]; ok {
		if t, ok := logTexts[lang][k]; ok {
			return t
		}
	}
	if strings.HasPrefix(s, ", ") { // e.g. the cleanup's ", 3599 GB free on the NAS"
		return translateLog(s, lang)
	}
	return s
}
