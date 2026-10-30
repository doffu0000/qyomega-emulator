// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.
// Soundfont engine for the emulator page: SpessaSynth (Apache-2.0, in vendor/) playing the
// SF2, SF3 or DLS files in soundfonts/. It has the same interface as WebAudioEngine in
// synth.js, plus rawSysex: GM and XG SysEx from the unit (XG system on, part mode, effects)
// goes to SpessaSynth unchanged, so it applies the XG rules itself. The soundfont only
// supplies the voices; it is not the unit's own sound.
import { WorkletSynthesizer } from 'spessasynth_lib';

const { VoiceMapper } = window.QYSynth;   // synth.js is loaded before this module

const NPARTS = 32;   // two ports of 16 parts

export class SoundfontEngine {
  constructor() {
    this.ctx = null; this.synth = null; this.master = null;
    this.enabled = false; this.volume = 0.7;
    this.font = null; this.channels = 16;
    this.mapper = new VoiceMapper(null);   // which preset plays each of the unit's voices, see tools/buildvoicemap.py
  }

  async enable() {
    if (!this.ctx) {
      this.ctx = new AudioContext();
      await this.ctx.audioWorklet.addModule('/vendor/spessasynth_lib/spessasynth_processor.min.js');
      this.master = this.ctx.createGain();
      this.master.gain.value = this.volume;
      this.master.connect(this.ctx.destination);
      this.synth = new WorkletSynthesizer(this.ctx);
      this.synth.connect(this.master);
      await this.synth.isReady;
    }
    await this.ctx.resume();
    this.enabled = true;
  }
  disable() {
    this.enabled = false;
    if (this.synth) this.synth.stopAll(true);
    if (this.ctx) this.ctx.suspend();
  }
  setVolume(v) {
    this.volume = v;
    if (this.master) this.master.gain.value = v;
  }

  // Loads a file from soundfonts/ in place of the current one (the same bank id, so it replaces).
  async loadFont(name) {
    if (!this.synth) throw new Error('sound is not started');
    if (this.font === name) return;
    const resp = await fetch('/soundfonts/' + encodeURIComponent(name));
    if (!resp.ok) throw new Error('cannot load ' + name + ': ' + resp.status);
    const buf = await resp.arrayBuffer();
    this.synth.stopAll(true);
    await this.synth.soundBankManager.addSoundBank(buf, 'main');
    this.font = name;
    // NAME.voicemap.json beside the font maps the unit's voices onto it; without one voices pass through
    let map = null;
    try {
      const r = await fetch('/soundfonts/' + encodeURIComponent(name.replace(/\.[^.]+$/, '') + '.voicemap.json'));
      if (r.ok) map = await r.json();
    } catch (e) { /* no map */ }
    this.mapper.setMap(map);
    this.hasVoiceMap = !!map;
  }

  ensure(part) {
    while (this.channels <= part && this.channels < NPARTS) { this.synth.addNewChannel(); this.channels++; }
  }
  live(part) {
    if (!this.synth || !this.enabled) return false;
    this.ensure(part);
    return true;
  }

  // the mapper always sees what the unit says (so its state is right when sound starts), the
  // synthesizer only gets messages while the engine is running
  send(msgs) {
    if (!this.synth || !this.enabled) return;
    for (const m of msgs) {
      this.ensure(m.part);
      if (m.k === 'pc') this.synth.programChange(m.part, m.v);
      else this.synth.controllerChange(m.part, m.n, m.v);
    }
  }
  noteOn(part, note, vel) { if (this.live(part)) this.synth.noteOn(part, note, vel); }
  noteOff(part, note) { if (this.live(part)) this.synth.noteOff(part, note); }
  program(part, v) { this.send(this.mapper.program(part, v)); }
  controller(part, cc, v) { this.send(this.mapper.controller(part, cc, v)); }
  bend(part, v) { if (this.live(part)) this.synth.pitchWheel(part, v + 8192); }
  // bytes: the message body and its closing F7, without the F0 (the library adds that itself)
  rawSysex(bytes) { if (this.synth && this.enabled) this.synth.systemExclusive(bytes); }
  partMode(part, v) { this.send(this.mapper.partMode(part, v)); }
  drumReset() {}
  reset() { this.mapper.reset(); if (this.synth) this.synth.stopAll(true); }
}
