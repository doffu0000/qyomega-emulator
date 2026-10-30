// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.
'use strict';
// Run with: node synth_test.js   (the Go suite runs it when node is installed)
const assert = require('assert');
const { StreamParser, VoiceMapper } = require('./synth.js');

function run(chunks) {
  const log = [];
  const rec = name => (...a) => log.push([name, ...a]);
  const eng = { noteOn: rec('on'), noteOff: rec('off'), program: rec('pc'), controller: rec('cc'),
                bend: rec('bend'), partMode: rec('mode'), reset: rec('reset'), drumReset: rec('drumReset') };
  const p = new StreamParser(eng);
  for (const c of chunks) p.feed(c);
  return log;
}
const hex = s => s.trim().split(/\s+/).map(x => parseInt(x, 16));

// what the main CPU sends for a keyboard note: a port select, running status off
assert.deepStrictEqual(run([hex('F5 01 90 3C 70 90 3C 00')]), [['on', 0, 60, 112], ['off', 0, 60]]);
// running status, note on with velocity 0 is a note off
assert.deepStrictEqual(run([hex('90 3C 70 3E 70 3E 00')]), [['on', 0, 60, 112], ['on', 0, 62, 112], ['off', 0, 62]]);
// the second port is parts 17 to 32
assert.deepStrictEqual(run([hex('F5 02 91 40 7F')]), [['on', 17, 64, 127]]);
// real time bytes inside a message and a message split over two feeds
assert.deepStrictEqual(run([hex('90 3C F8 70')]), [['on', 0, 60, 112]]);
assert.deepStrictEqual(run([hex('99 24'), hex('7F')]), [['on', 9, 36, 127]]);
// program change and controllers, one data byte and two
assert.deepStrictEqual(run([hex('C2 05 B0 00 7F B0 20 00')]), [['pc', 2, 5], ['cc', 0, 0, 127], ['cc', 0, 32, 0]]);
// pitch bend, 14 bit, centre is 0
assert.deepStrictEqual(run([hex('E0 00 40 E0 7F 7F')]), [['bend', 0, 0], ['bend', 0, 8191]]);
// XG system on and drum reset, as the unit sends at boot
assert.deepStrictEqual(run([hex('F0 43 10 4C 00 00 7E 00 F7 F0 43 10 4C 00 00 7D 00 F7')]), [['reset'], ['drumReset']]);
// XG multi part parameters
assert.deepStrictEqual(run([hex('F0 43 10 4C 08 09 07 01 F7')]), [['mode', 9, 1]]);
assert.deepStrictEqual(run([hex('F0 43 10 4C 08 03 03 28 F7')]), [['pc', 3, 40]]);
assert.deepStrictEqual(run([hex('F0 43 10 4C 08 02 01 7F F7 F0 43 10 4C 08 02 0B 64 F7')]), [['cc', 2, 0, 127], ['cc', 2, 7, 100]]);
// GM system on
assert.deepStrictEqual(run([hex('F0 7E 7F 09 01 F7')]), [['reset']]);
// SysEx for other models is ignored, and one cut short by a status byte is dropped
assert.deepStrictEqual(run([hex('F0 43 10 5F 10 00 00 01 F7')]), []);
assert.deepStrictEqual(run([hex('F0 43 10 4C 08 F0 43 10 4C 08 09 07 01 F7')]), [['mode', 9, 1]]);
assert.deepStrictEqual(run([hex('F0 43 10 4C 08 90 3C 70')]), [['on', 0, 60, 112]]);
// parts beyond 32 are clamped, data bytes without a status are ignored
assert.deepStrictEqual(run([hex('3C 70')]), []);

// ---- switching engines: the new engine is brought to the unit's current state
function recorder(raw) {
  const log = [];
  const rec = name => (...a) => log.push([name, ...a]);
  const eng = { noteOn: rec('on'), noteOff: rec('off'), program: rec('pc'), controller: rec('cc'),
                bend: rec('bend'), partMode: rec('mode'), reset: rec('reset'), drumReset: rec('drumReset') };
  if (raw) eng.rawSysex = rec('raw');
  return { eng, log };
}
{
  const a = recorder(false), b = recorder(false);
  const p = new StreamParser(a.eng);
  // before the XG system on: void afterwards
  p.feed(hex('B0 07 20 C0 07'));
  p.feed(hex('F0 43 10 4C 00 00 7E 00 F7'));
  // volume set twice, only the last survives; notes and note offs are never state
  p.feed(hex('B0 07 50 B0 07 64 C0 05 90 3C 70 80 3C 00 B0 7B 00 B0 78 00 E0 00 50'));
  p.feed(hex('F0 43 10 4C 08 09 07 01 F7'));
  p.feed(hex('F5 02 B1 0A 20 C1 09'));   // a part on the second port (17)
  p.setEngine(b.eng);
  assert.deepStrictEqual(b.log, [
    ['reset'],                         // the XG system on
    ['cc', 0, 7, 100], ['pc', 0, 5], ['bend', 0, 2048],   // 0x50 << 7 = 10240, less the centre 8192
    ['mode', 9, 1],
    ['cc', 17, 10, 32], ['pc', 17, 9],
  ]);
  // a power cycle clears the journal
  p.clearState();
  const c = recorder(false);
  p.setEngine(c.eng);
  assert.deepStrictEqual(c.log, []);
}
// ---- an engine with rawSysex gets GM and XG messages unchanged (body and F7, no F0), and a reset first
{
  const r = recorder(true);
  const p = new StreamParser(r.eng);
  p.feed(hex('F0 43 10 4C 00 00 7E 00 F7 F0 43 10 4C 02 01 40 01 00 F7 F0 43 10 5F 10 00 00 01 F7'));
  assert.deepStrictEqual(r.log, [
    ['reset'], ['raw', [0x43, 0x10, 0x4C, 0x00, 0x00, 0x7E, 0x00, 0xF7]],
    ['raw', [0x43, 0x10, 0x4C, 0x02, 0x01, 0x40, 0x01, 0x00, 0xF7]],   // effect type, forwarded as is
  ]);
  // replay into a second raw engine repeats them, the effect setting once even if sent twice
  p.feed(hex('F0 43 10 4C 02 01 40 02 00 F7'));
  const r2 = recorder(true);
  p.setEngine(r2.eng);
  assert.deepStrictEqual(r2.log, [
    ['reset'], ['raw', [0x43, 0x10, 0x4C, 0x00, 0x00, 0x7E, 0x00, 0xF7]],
    ['raw', [0x43, 0x10, 0x4C, 0x02, 0x01, 0x40, 0x02, 0x00, 0xF7]],
  ]);
}
// ---- the null engine accepts everything
{
  const p = new StreamParser();
  p.feed(hex('F5 01 90 3C 70 B0 07 64 F0 43 10 4C 00 00 7E 00 F7'));
}
// ---- the voice mapper
{
  const cc = (part, n, v) => ({ k: 'cc', part, n, v });
  const pc = (part, v) => ({ k: 'pc', part, v });
  const tweaks = (part, over) => [71, 72, 73, 74, 75, 91, 93].map(n => cc(part, n, Object.assign({ 71: 64, 72: 64, 73: 64, 74: 64, 75: 64, 91: 40, 93: 0 }, over)[n]));
  const MAP = {
    voices: {
      '0:40:0': { n: 'PianoStr', b: 11, p: 0 },                      // another preset
      '0:18:0': { n: 'MelloGrP', t: { 74: -18, 73: 8 } },            // the base preset, changed
      '64:0:32': { n: 'Shower', b: 1, p: 122 },                      // an SFX voice
    },
    drums: { '127:3': { p: 0, t: { 74: 18 } }, '127:9': { p: 8, t: { 74: -20, 91: 15 } }, '126:0': { p: 56 } },
  };
  // without a map everything goes through unchanged
  const plain = new VoiceMapper(null);
  assert.deepStrictEqual(plain.controller(0, 0, 3), [cc(0, 0, 3)]);
  assert.deepStrictEqual(plain.controller(0, 74, 90), [cc(0, 74, 90)]);
  assert.deepStrictEqual(plain.program(0, 5), [pc(0, 5)]);
  assert.deepStrictEqual(plain.partMode(0, 1), []);

  const m = new VoiceMapper(MAP);
  // bank selects wait for the program change; a variation maps to the soundfont's own preset
  assert.deepStrictEqual(m.controller(0, 0, 0), []);
  assert.deepStrictEqual(m.controller(0, 32, 40), []);
  assert.deepStrictEqual(m.program(0, 0), [cc(0, 0, 0), cc(0, 32, 11), pc(0, 0), ...tweaks(0, {})]);
  // a variation without its own preset is the base preset with tonal changes
  m.controller(1, 32, 18);
  assert.deepStrictEqual(m.program(1, 0), [cc(1, 0, 0), cc(1, 32, 0), pc(1, 0), ...tweaks(1, { 74: 46, 73: 72 })]);
  // the unit's own controller values are kept and the voice's changes are added to them
  assert.deepStrictEqual(m.controller(1, 74, 80), [cc(1, 74, 62)]);
  assert.deepStrictEqual(m.controller(1, 7, 100), [cc(1, 7, 100)]);        // other controllers untouched
  // another voice: the earlier changes are gone, the unit's value stays
  m.controller(1, 32, 0);
  assert.deepStrictEqual(m.program(1, 1), [cc(1, 0, 0), cc(1, 32, 0), pc(1, 1), ...tweaks(1, { 74: 80 })]);
  // a voice that is not in the map plays as the base voice of that program
  m.controller(2, 32, 99);
  assert.deepStrictEqual(m.program(2, 7), [cc(2, 0, 0), cc(2, 32, 0), pc(2, 7), ...tweaks(2, {})]);
  // SFX voices
  m.controller(3, 0, 64); m.controller(3, 32, 0);
  assert.deepStrictEqual(m.program(3, 32), [cc(3, 0, 0), cc(3, 32, 1), pc(3, 122), ...tweaks(3, {})]);
  // drum kits: bank 127 programs map to the soundfont's kits, with changes
  m.controller(4, 0, 127);
  assert.deepStrictEqual(m.program(4, 3), [cc(4, 0, 127), cc(4, 32, 0), pc(4, 0), ...tweaks(4, { 74: 82 })]);
  m.controller(5, 0, 126);
  assert.deepStrictEqual(m.program(5, 0), [cc(5, 0, 127), cc(5, 32, 0), pc(5, 56), ...tweaks(5, {})]);
  // part 10 of each port is drums by default, until the part mode says otherwise
  assert.deepStrictEqual(m.program(9, 9), [cc(9, 0, 127), cc(9, 32, 0), pc(9, 8), ...tweaks(9, { 74: 44, 91: 55 })]);
  assert.deepStrictEqual(m.program(25, 9), [cc(25, 0, 127), cc(25, 32, 0), pc(25, 8), ...tweaks(25, { 74: 44, 91: 55 })]);
  assert.deepStrictEqual(m.partMode(9, 0), [cc(9, 0, 0), cc(9, 32, 0), pc(9, 9), ...tweaks(9, {})]);
  assert.deepStrictEqual(m.partMode(0, 1)[0], cc(0, 0, 127));               // a normal part set to drums
  // values stay in range
  m.controller(6, 74, 120); m.controller(6, 32, 18);
  assert.deepStrictEqual(m.program(6, 0).slice(6, 7), [cc(6, 74, 102)]);
  m.controller(6, 74, 5);
  assert.deepStrictEqual(m.controller(6, 74, 5), [cc(6, 74, 0)]);
  // a reset forgets everything
  m.reset();
  assert.deepStrictEqual(m.program(0, 0), [cc(0, 0, 0), cc(0, 32, 0), pc(0, 0), ...tweaks(0, {})]);
}
// ---- XG voice selection by SysEx also reaches the engine, after the raw message
{
  const r = recorder(true);
  const p = new StreamParser(r.eng);
  p.feed(hex('F0 43 10 4C 08 02 01 00 F7 F0 43 10 4C 08 02 02 28 F7 F0 43 10 4C 08 02 03 05 F7 F0 43 10 4C 08 09 07 02 F7 F0 43 10 4C 08 02 0B 64 F7'));
  assert.deepStrictEqual(r.log.map(e => e[0] === 'raw' ? 'raw' : e.join(' ')), [
    'raw', 'cc 2 0 0', 'raw', 'cc 2 32 40', 'raw', 'pc 2 5', 'raw', 'mode 9 2', 'raw',   // volume is only raw
  ]);
}
console.log('synth parser: ok');
