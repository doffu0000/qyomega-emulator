# Third party code used by the emulator page

Copied unmodified from the npm packages, only the files the browser needs. The page resolves
the bare imports with an import map in `index.html`; nothing is fetched from the internet at
run time.

| Folder | Package | Version | Licence | Source |
| --- | --- | --- | --- | --- |
| `spessasynth_lib/` | spessasynth_lib | 4.4.0 | Apache-2.0 | https://github.com/spessasus/spessasynth_lib |
| `spessasynth_core/` | spessasynth_core | 4.4.1 | Apache-2.0 | https://github.com/spessasus/spessasynth_core |
| `fflate/` | fflate (`esm/browser.js`) | 0.8.3 | MIT | https://github.com/101arrowz/fflate |
| `stb-vorbis/` | stb-vorbis | 0.0.6 | Apache-2.0 | https://github.com/spessasus/stb-vorbis.js |

Each folder holds its licence text. SpessaSynth is the soundfont synthesizer (SF2, SF3, DLS)
used by the Sound menu; the worklet `spessasynth_processor.min.js` runs inside the audio
thread.

Soundfonts live in `../soundfonts/` and are listed automatically.
