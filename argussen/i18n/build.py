#!/usr/bin/env python3
"""Builds app/web/i18n.js from en.txt (the English source texts = keys) and <language>.txt (translations, same numbers).
Run: python3 app/i18n/build.py   – stops if a translation is missing or the {placeholders} do not match.
New language: copy a .txt file, translate the texts after |, and add the language to LANG_NAMES/LOC in app.js."""
import os, re, json, sys
D = os.path.dirname(os.path.abspath(__file__))
def read(f):
    out = {}
    for l in open(os.path.join(D, f), encoding='utf-8'):
        if l.startswith('#') or '|' not in l: continue
        k, v = l.rstrip('\n').split('|', 1)
        out[k] = v
    return out
en = read('en.txt')
SKIP = {'s25', 's26', 's27', 's29', 's30', 's31', 's32'}  # technical/partial errors, not translated
TRANS, errors = {}, []
for f in sorted(os.listdir(D)):
    if not f.endswith('.txt') or f == 'en.txt': continue
    lang, t = f[:-4], read(f)
    for k, src in en.items():
        if k in SKIP: continue
        if k not in t: errors.append(f'{lang}: missing {k} ({src})'); continue
        if sorted(re.findall(r'\{\w+\}', src)) != sorted(re.findall(r'\{\w+\}', t[k])): errors.append(f'{lang} {k}: placeholders do not match')
    TRANS[lang] = {en[k]: v for k, v in t.items() if k in en and k not in SKIP}
if errors:
    print('\n'.join(errors)); sys.exit(1)
with open(os.path.join(D, '..', 'web', 'i18n.js'), 'w', encoding='utf-8') as f:
    f.write('// Made by app/i18n/build.py from app/i18n/*.txt – edit there, not here.\n')
    f.write('window.TRANSLATIONS = ' + json.dumps(TRANS, ensure_ascii=False, indent=0, sort_keys=True) + ';\n')
print('i18n.js:', ', '.join(f'{k} ({len(v)})' for k, v in TRANS.items()))
