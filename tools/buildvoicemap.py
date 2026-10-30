#!/usr/bin/env python3
# Copyright (C) 2026 Doffu.
# SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.
"""Builds soundfonts/GeneralUser-GS.voicemap.json: how each XG voice is played on
GeneralUser GS.

The unit has 128 base voices, 352 variation voices (bank select LSB 1 and up), 45 SFX voices
(bank MSB 64) and 22 drum kits (MSB 127 and 126). GeneralUser GS only has the base voices, about
160 GS style variations and 13 drum kits, so most variations would fall back to the base voice and
sound identical. This table picks, for each unit voice, the closest preset in the soundfont and
adds small tonal changes on top (brightness, resonance, attack, release, chorus and reverb sends)
where only a base voice exists. It is a judgement from the voice names, not from hearing the
unit; edit the tables below and rerun to change it.

usage: buildvoicemap.py VOICES_JSON SOUNDFONT.sf2 OUT.json
  VOICES_JSON is the unit's voice list (program, bankMsb, bankLsb, name, bank), for example
  data/voices.json of the Doffu web controller.
Output entries are keyed "msb:lsb:program" (program 0 based) for voices and "msb:program" for drum
kits; "b" is the soundfont bank (sent as bank LSB, which is how SpessaSynth reaches a GS bank in XG
mode), "p" the program (0 based), "t" tweaks as {controller number: offset}.
"""
import collections
import json
import re
import struct
import sys

# ----------------------------------------------------------------------------------------
# What the soundfont has.
def soundfont_presets(path):
    data = open(path, 'rb').read()
    def chunks(pos, end):
        while pos + 8 <= end:
            cid = data[pos:pos + 4]
            size = struct.unpack('<I', data[pos + 4:pos + 8])[0]
            yield cid, pos + 8, size
            pos += 8 + size + (size & 1)
    for cid, p, size in chunks(12, len(data)):
        if cid == b'LIST' and data[p:p + 4] == b'pdta':
            for c2, p2, s2 in chunks(p + 4, p + size):
                if c2 == b'phdr':
                    out = {}
                    h = data[p2:p2 + s2]
                    for i in range(0, len(h) - 38, 38):
                        name = h[i:i + 20].split(b'\0')[0].decode('latin1')
                        prog, bank = struct.unpack('<HH', h[i + 20:i + 24])
                        out[(bank, prog)] = name
                    return out
    raise SystemExit('no preset headers in ' + path)

# ----------------------------------------------------------------------------------------
# Tweaks: cut = CC74 brightness (filter cutoff), res = CC71 resonance, atk = CC73 attack,
# rel = CC72 release, dec = CC75 decay, rev = CC91 reverb send, cho = CC93 chorus send.
TWEAK_CC = {'cut': 74, 'res': 71, 'atk': 73, 'rel': 72, 'dec': 75, 'rev': 91, 'cho': 93}

def parse_tweaks(tokens):
    t = {}
    for tok in tokens:
        m = re.fullmatch(r'(cut|res|atk|rel|dec|rev|cho)([+-]\d+)', tok)
        if not m:
            raise SystemExit('bad tweak %r' % tok)
        v = int(m.group(2))
        # Measured in SpessaSynth: brightness below the default (64) darkens hard and lowers the
        # level (a piano at 34 keeps a third of its brightness), above it the effect saturates
        # quickly. Negative cutoff offsets are therefore softened.
        if m.group(1) == 'cut' and v < 0:
            v = round(v * 0.45)
        t[str(TWEAK_CC[m.group(1)])] = v
    return t

# Rows: "program lsb target tweaks...". Program is 1 based as in the Data List. Target is "."
# for the base voice of that program, or "bank/program" of a preset in the soundfont (program 1
# based as printed by the soundfont tools). A voice ending in K (keyboard velocity version) is
# left as the base voice on purpose.
EXPLICIT = """
1 1 .
1 18 . cut-18 atk+8
1 40 11/1
1 41 11/2 rev+25
2 1 .
3 1 .
3 32 . cho+50 cut+5
3 40 11/2
3 41 11/1 cut+10
4 1 .
5 1 .
5 18 . cut-20
5 32 8/5
5 40 . cut+15 res+10
5 45 11/5
5 64 . cut-10 cho+25
6 1 .
6 32 8/6
6 33 . cut+12
6 34 . res+8 rev+20
6 40 8/6 cho+60
6 41 11/6
6 42 . cut+10 dec-15
6 45 11/5
7 1 .
7 25 8/7
7 35 11/7
8 1 .
8 27 . res+40 cut-15
8 64 . cut+10
8 65 . cut+25 res+10
11 64 . cut-10
12 1 .
12 45 11/12
13 1 .
13 64 . cut-25
13 97 . cut+10 rev+20
13 98 . cut-20 dec-15
15 96 8/15
15 97 9/15
16 35 . cho+40
16 96 . cut+10
16 97 . cut+5 rev+20
17 32 8/17
17 33 11/17 cut-5
17 34 . cut-10 atk+6
17 35 12/17
17 36 . cut+10
17 37 8/17 cut-10
17 38 . cut-15 res+8
17 40 . cut+20
17 64 . cut-30
17 65 12/17 cut+5
17 66 0/21
17 67 11/17 cut+10
18 24 11/18
18 32 8/18
18 33 12/18 cut+10
18 37 12/18
19 64 11/19 cho+40
19 65 11/19 cho+60 atk+8
19 66 . cho+30 cut+10
20 32 8/20
20 35 11/20
20 40 12/20 rev+50
20 64 . cut-10 cho+30
20 65 11/20 cut-10
21 40 11/21
22 32 8/22
23 32 . cut+10 res+10
24 64 . cut-5
25 16 . cut+10
25 25 . cut-15
25 43 . cut+25
25 96 8/25
26 16 . cut+10 cho+20
26 35 8/26
26 40 0/25 cut+15
26 41 . rev+30
26 96 16/26
27 18 . cut-20
27 32 . cut+10 res+10
27 96 8/27
28 32 8/28
28 65 12/28
28 66 12/28 cut-10 cho+25
29 40 8/29
29 41 . cut-10 rel-12
29 43 8/29 cut+15
29 45 0/27 cut-5
29 96 . cut+10 res+10
30 43 . res+30 cut+10
31 12 . cut-10
31 24 . cut+10
31 35 . cut+20 res+10
31 36 . cut-5
31 37 0/30
31 38 . res+12
31 40 8/31
31 41 8/32
31 43 0/30 cut-10
31 45 . cut-15
32 65 8/31
32 66 . cut+15
33 40 . cut-10
33 45 . cut+10
34 18 . cut-25
34 27 . cho+70 res+15
34 40 . cut+20
34 43 0/37
34 45 . cut+8
34 64 0/36
34 65 0/36 cho+40
34 112 . cut+20 res+8
35 28 . cut-20 rel-15
36 32 . cut+10
36 33 . cut-10 cho+20
36 34 0/34 cut-15
36 96 0/40 cut-10
36 97 . cut-20
37 27 . res+35 cut+10
37 32 . cut+10
37 64 0/38
37 65 . cut+5
38 43 0/37
39 18 . cut-25
39 20 . res+30 atk-8
39 24 8/39
39 27 . res+40
39 35 0/8
39 40 11/39
39 64 12/39
39 65 11/40
39 66 8/40
39 67 1/39
39 68 . res+30 cut+15
39 96 0/40 cut+15
39 112 1/39 cut-5
39 113 . res+40
40 6 . cut-20
40 12 0/40 cut+10
40 18 8/40
40 19 . cut-25
40 32 0/40 cut-10 atk+8
40 40 12/39
40 41 8/40 cut+10
40 64 . res+25
40 65 11/40
40 66 0/39 cut-10
40 67 . cut+20
40 112 . cut-15
40 113 . cut+5
40 114 12/39 cut-5
41 8 . atk+30
45 8 1/45 atk+10
45 40 . atk+20 rev+20
47 40 0/16
49 3 1/49
49 8 0/50
49 24 . cut-8
49 35 . cut-15 cho+30
49 40 8/49
49 41 12/49
49 42 0/45
49 45 11/50
50 3 1/50
50 8 . atk+15
50 40 . cut-15
50 41 12/50 rev+25
50 64 . cut-10 cho+40
50 65 0/49
51 27 . res+40
51 64 11/51
51 65 11/52
53 3 1/53
53 16 0/54
53 32 0/54 cut-5
53 40 . cho+25
55 40 0/86
55 41 0/53 cut-10
55 64 . cut-15 cho+25
56 35 . cut+10
56 64 . cut-10 rev+30
56 68 . cut-25
56 70 .
56 71 . cut+8
56 72 . cut+20
56 73 . cut+30
57 16 1/57
57 17 . cut+15
57 32 . cut-15
58 18 1/58
59 16 . cut-10
61 6 1/61
61 32 . cut-10
61 37 . atk+10 rev+25
62 3 1/62
62 35 8/62
62 40 11/62
62 41 . cut+20
62 42 . cut-20
63 12 . res+20
63 20 . res+40
63 24 8/63
63 27 8/64
63 32 0/64
63 40 . cut-15
63 45 8/63 cut+5
63 64 8/63
64 18 . cut-20
64 40 8/64
64 41 0/63 cho+30
64 45 8/64
64 64 8/64 cut-5
66 40 . cho+30
66 43 . cut+25
67 40 . cut-5 res+8
67 41 . cut-20
67 64 . cut+10
81 6 1/81
81 8 12/81
81 18 . cut-20
81 19 13/81
81 64 . cut-30
81 65 8/81
81 66 8/81 cut-5
81 67 12/81 cut+10
82 6 1/82
82 8 12/82
82 18 13/82
82 19 8/82
82 20 12/82 cho+30
82 24 13/82 cut+10
82 25 . res+40
82 27 . res+50
82 32 12/82 cho+50
82 35 8/82 cut+20
82 36 . cut-25
82 40 1/81
82 41 8/82
82 45 11/82
82 64 13/82 res+15
82 96 11/101
83 65 . cut-10
84 64 . res+12
84 65 . res+40 cut+15
85 64 . cut+10
85 65 . res+25
85 66 . dec-20
86 24 0/55
86 64 . cut-10
87 35 . cho+30
88 16 . cut-10
88 64 . res+20
88 65 . cut-25
89 64 12/89
90 16 . cho+30
90 17 . cut-20
90 18 . cut-35 atk+10
90 64 . cut-10
90 65 . cho+70
91 64 . cut-10
91 65 . atk-12
91 66 . cut+10
91 67 . cut+5 res+10
92 64 11/97
92 66 12/89
92 67 . cut-10
93 64 11/90
93 65 . cut+15
94 64 . cut-10
94 65 . cut+10
96 20 . res+25
96 27 . res+40
96 64 12/90
96 66 11/97
97 45 11/89
97 64 .
97 65 . rev+30
97 66 . cut+10
98 27 . res+25
98 64 . cut-10
99 12 . cut+10
99 14 1/99
99 18 0/10
99 35 0/10
99 40 0/10 cut-10
99 41 11/99
99 42 11/99 cho+40
99 64 1/99
99 65 . cut-20
99 66 0/10 cut+20
99 67 0/113
99 68 0/12
99 69 11/9
99 70 12/11
99 71 0/47
99 72 0/13
100 18 . cut-15
100 19 . cut-10
100 40 0/5 cut-10
100 64 0/25 rev+30
100 65 0/47
100 66 13/89
100 67 12/90
101 64 . cut+10
101 96 11/101
102 64 11/90
102 65 12/89
102 66 13/89
102 67 12/90
102 68 11/97
102 70 13/89 cut-10
102 71 11/99
102 96 0/53 cho+30
103 8 . cut-10
103 14 2/103
103 64 .
103 65 2/103 cho+30
103 66 . cut-5
103 67 12/90
103 68 11/99
103 69 2/103 res+25
104 64 . cut+10
105 32 . cho+40
105 35 . cut+10
105 96 . cut-10
105 97 . res+10
106 28 . cut-15 rel-10
106 96 0/105
106 97 . cut-5
106 98 0/108
108 96 8/108
108 97 0/16
112 64 . cut+10
112 96 . res+15
112 97 . cut+15
113 96 . cut-10
113 97 .
113 98 0/115
113 99 0/115 cut+10
113 100 12/120
113 101 0/15
115 97 0/10
115 98 0/114
116 96 8/116
117 96 8/117
118 64 8/118
118 65 . cut+10
118 66 . cut+20
119 64 8/119
119 65 . cut+15
"""

# SFX voices (bank MSB 64): program (1 based, as in the Data List) -> soundfont "bank/program".
SFX = """
1 1/121
2 1/121 cut-20
3 1/121 cut+20
4 2/121
5 0/121
6 0/121 cut-15
17 1/122
33 1/123
34 2/123
35 3/123
36 4/123
37 5/123
38 0/124
49 1/124
50 2/124
51 3/124
55 11/123
56 11/128
65 0/125
66 2/125
67 3/125
68 4/125
69 4/125 cut+15
70 5/125
71 1/125
81 1/126
82 2/126
83 3/126
84 4/126
85 5/126
86 6/126
87 7/126
88 8/126
89 9/126
90 0/126
91 12/123 cut-15
97 1/127
98 2/127
99 3/127
100 4/127
101 5/127
113 1/128
114 2/128
115 3/128
116 12/128
"""

# Drum kits: XG drum kit program (0 based) -> soundfont drum kit program (0 based), tweaks.
# GeneralUser GS kits: 0 standard 1, 1 standard 2, 2 standard 3, 8 room, 16 power, 24 electronic,
# 25 808/909, 26 dance, 32 jazz, 40 brush, 48 orchestral, 56 sfx, 127 CM-64/32L.
DRUMS = """
127 0 0
127 1 1
127 2 2 cut-8
127 3 0 cut+18
127 8 8
127 9 8 cut-20 rev+15
127 16 16
127 17 16 cut+12
127 24 24
127 25 25
127 26 25 cut+10
127 27 26
127 28 25 cut-10
127 29 26 cut+12
127 32 32
127 33 32 cut-10
127 40 40
127 48 48
127 112 2 cut+10
127 113 16 cut-8
126 0 56
126 1 56 cut+10
"""

# Words in a voice name that say how to change the base voice when no row above covers it.
KEYWORDS = [
    (r'mello|soft|warm|dark|dk$|smooth|smth|light', ('cut-18', 'atk+6')),
    (r'bright|brite|hard|hi |pierce', ('cut+15',)),
    (r'reso|rez|acid|wasp|wah', ('res+30', 'cut+5')),
    (r'chor|det|choral|doubl|dbl', ('cho+45',)),
    (r'slow|legato|susp', ('atk+20', 'rel+10')),
    (r'fast|click|clk|punch|pick', ('atk-8',)),
    (r'mute', ('cut-12', 'rel-15')),
    (r'echo|reverb|space|dream', ('rev+35',)),
]
# Last resort, so voices without a hint still differ from the base voice.
SPREAD = [
    ('cut+12',), ('cut-12',), ('res+15',), ('atk+12', 'rel+8'),
    ('cut+8', 'cho+25'), ('cut-8', 'rev+20'), ('res+10', 'cut-5'), ('dec-12', 'cut+5'),
]


def main():
    voices_path, sf_path, out_path = sys.argv[1:4]
    voices = json.load(open(voices_path, encoding='utf-8'))
    presets = soundfont_presets(sf_path)

    def check(bank, prog1, where):
        if (bank, prog1 - 1) not in presets:
            raise SystemExit('%s: the soundfont has no preset %d/%d' % (where, bank, prog1))

    out = {'voices': {}, 'drums': {}, 'source': 'tools/buildvoicemap.py'}
    how = collections.Counter()
    explicit = {}
    for line in EXPLICIT.strip().splitlines():
        parts = line.split()
        if not parts:
            continue
        prog, lsb, target, *tw = parts
        explicit[(int(prog), int(lsb))] = (target, parse_tweaks(tw))

    for v in voices:
        if v['bank'] == 'normal':
            prog1, lsb = v['program'], v['bankLsb']
            key = '0:%d:%d' % (lsb, prog1 - 1)
            if lsb == 0:
                continue                                  # the base voices play as they are
            if (prog1, lsb) in explicit:
                target, tweaks = explicit[(prog1, lsb)]
                how['explicit'] += 1
            else:
                tw = []
                for pat, t in KEYWORDS:
                    if re.search(pat, v['name'], re.I):
                        tw = list(t)
                        break
                if tw:
                    how['keyword'] += 1
                else:
                    tw = list(SPREAD[(prog1 * 131 + lsb * 17) % len(SPREAD)])
                    how['spread'] += 1
                target, tweaks = '.', parse_tweaks(tw)
                print('not in the table (%s): %d:%d %s -> %s' % (how.most_common(1)[0][0] if False else 'auto', prog1, lsb, v['name'], ' '.join(tw)))
            entry = {'n': v['name']}
            if target != '.':
                bank, p1 = (int(x) for x in target.split('/'))
                check(bank, p1, key)
                entry['b'], entry['p'] = bank, p1 - 1
            if tweaks:
                entry['t'] = tweaks
            out['voices'][key] = entry
        elif v['bank'] == 'sfx':
            row = [r for r in SFX.strip().splitlines() if r.split()[0] == str(v['program'])]
            if not row:
                raise SystemExit('no SFX row for %s' % v)
            parts = row[0].split()
            bank, p1 = (int(x) for x in parts[1].split('/'))
            check(bank, p1, 'sfx %d' % v['program'])
            entry = {'n': v['name'], 'b': bank, 'p': p1 - 1}
            if parts[2:]:
                entry['t'] = parse_tweaks(parts[2:])
            out['voices']['64:0:%d' % (v['program'] - 1)] = entry
            how['sfx'] += 1

    kits = {(int(l.split()[0]), int(l.split()[1])): l.split()[2:] for l in DRUMS.strip().splitlines()}
    for v in voices:
        if v['bank'] in ('drum', 'sfxkit'):
            row = kits.get((v['bankMsb'], v['program'] - 1))
            if row is None:
                raise SystemExit('no drum kit row for %s' % v)
            gu = int(row[0])
            if (120, gu) not in presets:
                raise SystemExit('the soundfont has no drum kit %d' % gu)
            entry = {'n': v['name'], 'p': gu}
            if row[1:]:
                entry['t'] = parse_tweaks(row[1:])
            out['drums']['%d:%d' % (v['bankMsb'], v['program'] - 1)] = entry
            how['drum'] += 1

    json.dump(out, open(out_path, 'w'), indent=0, sort_keys=True)
    print(dict(how), 'voices', len(out['voices']), 'drums', len(out['drums']))


if __name__ == '__main__':
    main()
