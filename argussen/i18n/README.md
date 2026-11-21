# Translations

The user interface texts are written in English in the code (`tr('…')` in `web/app.js`) and used as keys.

| File | Contents |
|---|---|
| `en.txt` | The English source texts, one per line: `number|text` (numbers starting with `s` are server error messages) |
| `da.txt`, `de.txt`, `fr.txt`, `sv.txt`, `nb.txt`, `nl.txt` | The translations, with the same numbers |
| `build.py` | Builds `web/i18n.js` and checks that nothing is missing and that `{placeholders}` match |

After changing a translation, run `python3 i18n/build.py`.

New language: copy a `.txt` file, translate the texts after `|`, and add the language to `LANG_NAMES`/`LOC` in `web/app.js`.
