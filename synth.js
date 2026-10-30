// Copyright (C) 2026 Doffu.
// SPDX-License-Identifier: GPL-3.0-only. See LICENSE and NOTICE.md.
'use strict';
// Sound for the emulator page. The main CPU sends the sound CPU plain MIDI and XG
// SysEx through a byte port (`F5 nn` picks the port, then MIDI with running status); the
// emulator serves those bytes on /tg. There is no hardware tone generator or wave ROM here, so
// this plays them on a small General MIDI style synthesizer built on WebAudio: the voices are
// generic, not the unit's samples.
//
// Two layers: StreamParser turns bytes into channel events and XG parameter changes, and an
// engine makes sound from them. The engine is the part to swap for a soundfont player; it
// only needs noteOn, noteOff, program, controller, bend, partMode, reset and allOff.

(function () {
  const NPARTS = 32;
  const mtof = (n, cents) => 440 * Math.pow(2, (n - 69) / 12 + (cents || 0) / 1200);

  // ---------------------------------------------------------------- stream parser
  // Receives nothing and plays nothing: used while sound is off.
  const NULL_ENGINE = { noteOn() {}, noteOff() {}, program() {}, controller() {}, bend() {},
                        partMode() {}, reset() {}, drumReset() {} };

  // What kind of SysEx this is, for the parts of it that matter here: 'gm' (GM system on),
  // 'xgon', 'xgdrum', other XG parameter changes as 'xg', anything else null.
  function sysexKind(sx) {
    if (sx.length >= 4 && sx[0] === 0x7E && sx[2] === 0x09 && sx[3] === 0x01) return 'gm';
    if (sx.length < 7 || sx[0] !== 0x43 || (sx[1] & 0xF0) !== 0x10 || sx[2] !== 0x4C) return null;
    if (sx[3] === 0x00 && sx[4] === 0x00) {
      if (sx[5] === 0x7E) return 'xgon';
      if (sx[5] === 0x7D) return 'xgdrum';
    }
    return 'xg';
  }

  class StreamParser {
    constructor(engine) {
      this.eng = engine || NULL_ENGINE;
      // The latest controller, program, bend and XG SysEx values since the last system on, in
      // the order they arrived, so a new engine can be brought to the unit's current state.
      this.journal = new Map();
      this.seq = 0;
      this.replaying = false;
      this.reset();
    }
    // forget a half received message (the byte stream starts over after a power cycle)
    reset() {
      this.status = 0; this.need = 0; this.data = [];
      this.sx = null; this.f5 = false; this.port = 0;
    }
    clearState() { this.journal.clear(); }
    // Switches the engine and plays the journal into it.
    setEngine(engine) {
      this.eng = engine || NULL_ENGINE;
      this.replaying = true;
      try {
        for (const e of this.journal.values()) {
          if (e.kind === 'sx') this.sysex(e.bytes);
          else this.dispatch(e.status, e.data, e.part);
        }
      } finally { this.replaying = false; }
    }
    remember(key, entry) {
      this.journal.delete(key);          // a later value replaces an earlier one and moves to the end
      this.journal.set(key, entry);
      if (this.journal.size > 4000) this.journal.delete(this.journal.keys().next().value);
    }
    feed(bytes) {
      for (const b of bytes) this.byte(b);
    }
    byte(b) {
      if (this.sx) {
        if (b < 0x80) { this.sx.push(b); return; }
        if (b >= 0xF8) return;               // real time bytes may sit inside a message
        const sx = this.sx; this.sx = null;
        if (b === 0xF7) { this.sysex(sx); return; }
        // anything else ends the message without its F7: drop it and handle b normally
      }
      if (this.f5) { this.port = Math.max(0, b - 1); this.f5 = false; return; }
      if (b >= 0xF8) return;
      if (b === 0xF0) { this.sx = []; this.status = 0; return; }
      if (b === 0xF5) { this.f5 = true; this.status = 0; return; }
      if (b >= 0xF1) { this.status = 0; return; }
      if (b >= 0x80) {
        this.status = b; this.data = [];
        const hi = b >> 4;
        this.need = (hi === 0xC || hi === 0xD) ? 1 : 2;
        return;
      }
      if (!this.status) return;
      this.data.push(b);
      if (this.data.length === this.need) {
        const d = this.data; this.data = [];
        this.message(this.status, d);
      }
    }
    message(status, d) {
      const part = Math.min(NPARTS - 1, (status & 15) + this.port * 16);
      const type = status >> 4;
      if (!this.replaying) {
        // notes are not state; "all notes off" and "all sound off" are not either
        if (type === 0xC) this.remember('pc:' + part, { kind: 'ch', status, data: d.slice(), part });
        else if (type === 0xE) this.remember('bend:' + part, { kind: 'ch', status, data: d.slice(), part });
        else if (type === 0xB && d[0] !== 120 && d[0] !== 123) this.remember('cc:' + part + ':' + d[0], { kind: 'ch', status, data: d.slice(), part });
      }
      this.dispatch(status, d, part);
    }
    dispatch(status, d, part) {
      const e = this.eng;
      switch (status >> 4) {
        case 0x8: e.noteOff(part, d[0]); break;
        case 0x9: if (d[1] === 0) e.noteOff(part, d[0]); else e.noteOn(part, d[0], d[1]); break;
        case 0xB: e.controller(part, d[0], d[1]); break;
        case 0xC: e.program(part, d[0]); break;
        case 0xE: e.bend(part, (d[0] | d[1] << 7) - 8192); break;
      }
    }
    sysex(sx) {
      const kind = sysexKind(sx);
      if (!kind) return;
      const e = this.eng;
      if (!this.replaying) {
        if (kind === 'gm' || kind === 'xgon') this.journal.clear();   // everything before it is void
        this.remember(kind === 'xg' ? 'xg:' + sx[3] + ':' + sx[4] + ':' + sx[5] : 'sx' + this.seq++, { kind: 'sx', bytes: sx.slice() });
      }
      if (e.rawSysex) {                       // a soundfont engine applies GM and XG itself
        if (kind === 'gm' || kind === 'xgon') e.reset();
        e.rawSysex([...sx, 0xF7]);             // the body and the closing F7, no F0 (SpessaSynth adds it)
        // voice selection also goes through the engine's voice map, which needs to know about it
        if (kind === 'xg' && sx[3] === 0x08 && sx[4] < NPARTS) {
          const part = sx[4], v = sx[6];
          switch (sx[5]) {
            case 0x01: e.controller(part, 0, v); break;
            case 0x02: e.controller(part, 32, v); break;
            case 0x03: e.program(part, v); break;
            case 0x07: e.partMode(part, v); break;
          }
        }
        return;
      }
      if (kind === 'gm' || kind === 'xgon') { e.reset(); return; }
      if (kind === 'xgdrum') { e.drumReset(); return; }
      const a1 = sx[3], a2 = sx[4], a3 = sx[5], v = sx[6];
      if (a1 === 0x08 && a2 < NPARTS) {        // multi part
        switch (a3) {
          case 0x01: e.controller(a2, 0, v); break;     // bank select MSB
          case 0x02: e.controller(a2, 32, v); break;    // bank select LSB
          case 0x03: e.program(a2, v); break;
          case 0x07: e.partMode(a2, v); break;           // 0 normal, anything else drums
          case 0x0B: e.controller(a2, 7, v); break;
          case 0x0E: e.controller(a2, 10, v); break;
        }
      }
    }
  }

  // ---------------------------------------------------------------- timbres
  // [type, detune in cents, gain] oscillators, ADSR in seconds, low pass cutoff and how much
  // extra cutoff the attack adds (decaying over the decay time).
  const FAMILIES = [
    /* 0 piano       */ { osc: [['triangle', 0, 1], ['sine', 1200, 0.3]], a: 0.003, d: 1.6, s: 0.12, r: 0.3, lp: 2600, lpEnv: 3000 },
    /* 1 chrom perc  */ { osc: [['sine', 0, 1], ['sine', 1900, 0.35]], a: 0.001, d: 1.1, s: 0.0, r: 0.4, lp: 6000, lpEnv: 0 },
    /* 2 organ       */ { osc: [['square', 0, 0.6], ['sine', 1200, 0.5]], a: 0.01, d: 0.05, s: 1.0, r: 0.06, lp: 2800, lpEnv: 0 },
    /* 3 guitar      */ { osc: [['sawtooth', 0, 1]], a: 0.002, d: 0.7, s: 0.15, r: 0.2, lp: 900, lpEnv: 2800 },
    /* 4 bass        */ { osc: [['sawtooth', 0, 0.8], ['square', -1200, 0.7]], a: 0.003, d: 0.3, s: 0.5, r: 0.09, lp: 500, lpEnv: 1500 },
    /* 5 strings     */ { osc: [['sawtooth', -7, 0.7], ['sawtooth', 7, 0.7]], a: 0.12, d: 0.3, s: 0.85, r: 0.4, lp: 2400, lpEnv: 0 },
    /* 6 ensemble    */ { osc: [['sawtooth', -9, 0.6], ['triangle', 9, 0.8]], a: 0.09, d: 0.3, s: 0.85, r: 0.4, lp: 2200, lpEnv: 0 },
    /* 7 brass       */ { osc: [['sawtooth', 0, 1], ['sawtooth', 5, 0.5]], a: 0.05, d: 0.2, s: 0.75, r: 0.12, lp: 1100, lpEnv: 2800 },
    /* 8 reed        */ { osc: [['square', 0, 0.8]], a: 0.03, d: 0.15, s: 0.75, r: 0.1, lp: 1700, lpEnv: 900 },
    /* 9 pipe        */ { osc: [['sine', 0, 1], ['triangle', 1200, 0.2]], a: 0.05, d: 0.08, s: 0.85, r: 0.12, lp: 5000, lpEnv: 0 },
    /* 10 synth lead */ { osc: [['sawtooth', -5, 0.7], ['square', 5, 0.5]], a: 0.005, d: 0.15, s: 0.7, r: 0.1, lp: 3500, lpEnv: 1500 },
    /* 11 synth pad  */ { osc: [['sawtooth', -10, 0.6], ['sawtooth', 10, 0.6]], a: 0.35, d: 0.4, s: 0.85, r: 0.6, lp: 1600, lpEnv: 0 },
    /* 12 synth fx   */ { osc: [['triangle', 0, 0.9], ['sine', 700, 0.4]], a: 0.2, d: 0.4, s: 0.6, r: 0.7, lp: 3000, lpEnv: 0 },
    /* 13 ethnic     */ { osc: [['triangle', 0, 1], ['sawtooth', 1200, 0.2]], a: 0.002, d: 0.5, s: 0.08, r: 0.18, lp: 2200, lpEnv: 2000 },
    /* 14 percussive */ { osc: [['sine', 0, 1], ['triangle', 1200, 0.4]], a: 0.001, d: 0.3, s: 0.0, r: 0.12, lp: 5000, lpEnv: 0 },
    /* 15 sound fx   */ { osc: [['sawtooth', 0, 0.5], ['square', 350, 0.4]], a: 0.05, d: 0.5, s: 0.3, r: 0.5, lp: 1200, lpEnv: 1500 },
  ];

  // General MIDI drum map to the sounds below; anything else is a short pitched blip.
  const DRUM_KIND = {
    35: 'kick', 36: 'kick', 37: 'rim', 38: 'snare', 39: 'clap', 40: 'snare',
    41: 'tom', 43: 'tom', 45: 'tom', 47: 'tom', 48: 'tom', 50: 'tom',
    42: 'hat', 44: 'hat', 46: 'openhat', 49: 'crash', 51: 'ride', 52: 'crash', 53: 'ride',
    54: 'hat', 55: 'crash', 56: 'cowbell', 57: 'crash', 59: 'ride', 69: 'hat', 70: 'hat',
  };

  // ---------------------------------------------------------------- engine
  class WebAudioEngine {
    constructor() {
      this.ctx = null; this.master = null; this.noise = null;
      this.enabled = false; this.volume = 0.7;
      this.voices = [];
      this.parts = [];
      for (let i = 0; i < NPARTS; i++) this.parts.push(this.newPart(i));
    }
    newPart(i) {
      return { bank: 0, lsb: 0, program: 0, vol: 100, expr: 127, pan: 64, sustain: false, bend: 0,
               mode: null, node: null, panner: null, index: i };
    }
    isDrum(p) {
      if (p.mode !== null) return p.mode !== 0;
      return p.bank === 127 || p.bank === 126 || p.index % 16 === 9;
    }

    // -- control from the UI
    async enable() {
      if (!this.ctx) {
        const Ctx = window.AudioContext || window.webkitAudioContext;
        this.ctx = new Ctx();
        this.master = this.ctx.createGain();
        this.master.gain.value = this.volume;
        const comp = this.ctx.createDynamicsCompressor();
        this.master.connect(comp); comp.connect(this.ctx.destination);
        const len = this.ctx.sampleRate * 2, buf = this.ctx.createBuffer(1, len, this.ctx.sampleRate), ch = buf.getChannelData(0);
        for (let i = 0; i < len; i++) ch[i] = Math.random() * 2 - 1;
        this.noise = buf;
        for (const p of this.parts) p.node = null;
      }
      await this.ctx.resume();
      this.enabled = true;
    }
    disable() {
      this.allOff(true);
      this.enabled = false;
      if (this.ctx) this.ctx.suspend();
    }
    setVolume(v) {
      this.volume = v;
      if (this.master) this.master.gain.value = v;
    }

    // -- events from the parser (state is tracked even while sound is off)
    reset() {
      this.allOff(true);
      for (const p of this.parts) if (p.panner) { try { p.panner.disconnect(); } catch (e) { /* gone */ } }
      this.parts = this.parts.map((p, i) => this.newPart(i));
    }
    drumReset() { for (const p of this.parts) p.mode = null; }
    partMode(i, v) { this.parts[i].mode = v; }
    program(i, v) { this.parts[i].program = v; }
    controller(i, cc, v) {
      const p = this.parts[i];
      switch (cc) {
        case 0: p.bank = v; break;
        case 32: p.lsb = v; break;
        case 7: p.vol = v; this.applyPart(p); break;
        case 11: p.expr = v; this.applyPart(p); break;
        case 10: p.pan = v; this.applyPart(p); break;
        case 64:
          p.sustain = v >= 64;
          if (!p.sustain) for (const vo of this.voices) if (vo.part === i && vo.sustained) this.release(vo);
          break;
        case 120: this.allOff(true, i); break;
        case 123: for (const vo of this.voices.slice()) if (vo.part === i) this.release(vo); break;
        case 121: p.expr = 127; p.sustain = false; p.bend = 0; this.applyPart(p); break;
      }
    }
    bend(i, v) {
      const p = this.parts[i]; p.bend = v;
      if (!this.enabled) return;
      for (const vo of this.voices) {
        if (vo.part !== i) continue;
        for (const o of vo.oscs) o.osc.detune.value = o.cents + v / 8192 * 200;
      }
    }

    // -- sound
    partNode(p) {
      if (!p.node) {
        p.node = this.ctx.createGain();
        p.panner = this.ctx.createStereoPanner();
        p.node.connect(p.panner); p.panner.connect(this.master);
        this.applyPart(p);
      }
      return p.node;
    }
    applyPart(p) {
      if (!p.node) return;
      p.node.gain.value = (p.vol / 127) * (p.expr / 127);
      p.panner.pan.value = p.pan === 0 ? 0 : Math.max(-1, Math.min(1, (p.pan - 64) / 63));
    }
    noteOn(i, note, vel) {
      if (!this.enabled) return;
      const p = this.parts[i];
      const out = this.partNode(p);
      if (this.isDrum(p)) { this.drum(out, note, vel); return; }
      const ctx = this.ctx, t = ctx.currentTime;
      const spec = FAMILIES[p.program >> 3];
      const amp = Math.pow(vel / 127, 1.4) * 0.55;
      const g = ctx.createGain();
      const filt = ctx.createBiquadFilter();
      filt.type = 'lowpass'; filt.Q.value = 0.8;
      const hi = Math.min(18000, spec.lp + spec.lpEnv * (vel / 127) + note * 6);
      filt.frequency.setValueAtTime(hi, t);
      if (spec.lpEnv) filt.frequency.setTargetAtTime(Math.min(18000, spec.lp + note * 6), t, spec.d / 3);
      g.gain.setValueAtTime(0, t);
      g.gain.linearRampToValueAtTime(amp, t + spec.a);
      g.gain.setTargetAtTime(amp * spec.s, t + spec.a, spec.d / 3);
      filt.connect(g); g.connect(out);
      const oscs = [];
      for (const [type, cents, gain] of spec.osc) {
        const osc = ctx.createOscillator(), og = ctx.createGain();
        osc.type = type; osc.frequency.value = mtof(note);
        const c = cents; osc.detune.value = c + p.bend / 8192 * 200;
        og.gain.value = gain / spec.osc.length;
        osc.connect(og); og.connect(filt);
        osc.start(t);
        oscs.push({ osc, cents: c });
      }
      if (this.voices.length >= 40) this.kill(this.voices[0]);
      this.voices.push({ part: i, note, g, filt, oscs, spec, released: false, sustained: false });
    }
    noteOff(i, note) {
      if (!this.enabled) return;
      const p = this.parts[i];
      for (const vo of this.voices) {
        if (vo.part === i && vo.note === note && !vo.released && !vo.sustained) {
          if (p.sustain) vo.sustained = true; else this.release(vo);
          return;
        }
      }
    }
    release(vo) {
      if (vo.released) return;
      vo.released = true;
      const t = this.ctx.currentTime, r = vo.spec.r;
      if (vo.g.gain.cancelAndHoldAtTime) vo.g.gain.cancelAndHoldAtTime(t);
      else vo.g.gain.cancelScheduledValues(t);
      vo.g.gain.setTargetAtTime(0, t, r / 3);
      for (const o of vo.oscs) o.osc.stop(t + r * 1.6 + 0.05);
      setTimeout(() => this.forget(vo), (r * 1.6 + 0.2) * 1000);
    }
    kill(vo) {
      if (!vo) return;
      try { for (const o of vo.oscs) o.osc.stop(); } catch (e) { /* already stopped */ }
      try { vo.g.disconnect(); } catch (e) { /* already gone */ }
      this.forget(vo);
    }
    forget(vo) {
      const k = this.voices.indexOf(vo);
      if (k >= 0) this.voices.splice(k, 1);
    }
    allOff(hard, only) {
      for (const vo of this.voices.slice()) {
        if (only !== undefined && vo.part !== only) continue;
        if (hard) this.kill(vo); else this.release(vo);
      }
    }

    // -- drums: all synthesized, short and self stopping
    noiseBurst(out, t, dur, type, freq, q, amp) {
      const ctx = this.ctx, src = ctx.createBufferSource(), f = ctx.createBiquadFilter(), g = ctx.createGain();
      src.buffer = this.noise; f.type = type; f.frequency.value = freq; f.Q.value = q;
      g.gain.setValueAtTime(amp, t); g.gain.exponentialRampToValueAtTime(0.0008, t + dur);
      src.connect(f); f.connect(g); g.connect(out);
      src.start(t, Math.random() * 1.5); src.stop(t + dur + 0.02);
    }
    sweep(out, t, f0, f1, dur, type, amp) {
      const ctx = this.ctx, o = ctx.createOscillator(), g = ctx.createGain();
      o.type = type; o.frequency.setValueAtTime(f0, t); o.frequency.exponentialRampToValueAtTime(Math.max(20, f1), t + dur);
      g.gain.setValueAtTime(amp, t); g.gain.exponentialRampToValueAtTime(0.0008, t + dur);
      o.connect(g); g.connect(out); o.start(t); o.stop(t + dur + 0.02);
    }
    drum(out, note, vel) {
      const t = this.ctx.currentTime, a = Math.pow(vel / 127, 1.3) * 0.7;
      switch (DRUM_KIND[note] || 'blip') {
        case 'kick': this.sweep(out, t, 160, 42, 0.32, 'sine', a * 1.2); this.noiseBurst(out, t, 0.02, 'highpass', 2500, 0.7, a * 0.25); break;
        case 'snare': this.sweep(out, t, 210, 150, 0.12, 'triangle', a * 0.5); this.noiseBurst(out, t, 0.2, 'highpass', 1500, 0.7, a * 0.8); break;
        case 'rim': this.sweep(out, t, 900, 700, 0.04, 'square', a * 0.35); this.noiseBurst(out, t, 0.03, 'bandpass', 2500, 1.5, a * 0.4); break;
        case 'clap': for (let k = 0; k < 3; k++) this.noiseBurst(out, t + k * 0.012, 0.06, 'bandpass', 1500, 1.2, a * 0.6); this.noiseBurst(out, t + 0.036, 0.18, 'bandpass', 1500, 1.2, a * 0.5); break;
        case 'tom': this.sweep(out, t, mtof(note) * 2.2, mtof(note) * 0.9, 0.35, 'sine', a * 1.0); break;
        case 'hat': this.noiseBurst(out, t, 0.05, 'highpass', 7000, 0.7, a * 0.5); break;
        case 'openhat': this.noiseBurst(out, t, 0.4, 'highpass', 6500, 0.7, a * 0.5); break;
        case 'crash': this.noiseBurst(out, t, 1.1, 'highpass', 5000, 0.5, a * 0.5); break;
        case 'ride': this.noiseBurst(out, t, 0.6, 'highpass', 6000, 0.6, a * 0.35); this.sweep(out, t, 3300, 3300, 0.5, 'sine', a * 0.12); break;
        case 'cowbell': this.sweep(out, t, 800, 800, 0.25, 'square', a * 0.3); this.sweep(out, t, 540, 540, 0.25, 'square', a * 0.3); break;
        default: this.sweep(out, t, mtof(Math.min(108, note + 12)), mtof(Math.min(108, note + 12)), 0.12, 'sine', a * 0.3);
      }
    }
  }

  // ---------------------------------------------------------------- voice mapping
  // The unit selects voices the XG way (bank MSB and LSB plus program) and has 352 variation
  // voices; a General MIDI style soundfont has far fewer, so left alone most variations play as
  // the base voice and sound alike. A voice map (soundfonts/NAME.voicemap.json, made by
  // tools/buildvoicemap.py) says which soundfont preset plays each unit voice and which tonal
  // changes to add. The mapper keeps what the unit last said for each part and returns the
  // messages for the synthesizer: {k:'cc', part, n, v} and {k:'pc', part, v}. Without a map it
  // passes everything through.
  const TWEAK_CCS = [71, 72, 73, 74, 75, 91, 93];   // resonance, release, attack, cutoff, decay, reverb, chorus
  const TWEAK_DEFAULT = { 71: 64, 72: 64, 73: 64, 74: 64, 75: 64, 91: 40, 93: 0 };
  const clamp7 = v => Math.max(0, Math.min(127, v));
  const ccMsg = (part, n, v) => ({ k: 'cc', part, n, v });
  const pcMsg = (part, v) => ({ k: 'pc', part, v });

  class VoiceMapper {
    constructor(map) {
      this.map = map || null;
      this.reset();
    }
    setMap(map) { this.map = map || null; }
    reset() {
      this.parts = [];
      for (let i = 0; i < NPARTS; i++) this.parts.push({ msb: 0, lsb: 0, pc: 0, mode: null, cc: Object.assign({}, TWEAK_DEFAULT), tweaks: {} });
    }
    isDrum(part, st) {
      if (st.msb === 127 || st.msb === 126) return true;
      if (st.mode !== null) return st.mode > 0;
      return part % 16 === 9;                       // XG: part 10 of each port is drums by default
    }
    // the unit's own controller values are kept, the voice's tweaks are added on top
    effective(st, n) { return clamp7(st.cc[n] + (st.tweaks[n] || 0)); }

    controller(part, cc, v) {
      const st = this.parts[part];
      if (cc === 0) { st.msb = v; return this.map ? [] : [ccMsg(part, cc, v)]; }    // held until the program change
      if (cc === 32) { st.lsb = v; return this.map ? [] : [ccMsg(part, cc, v)]; }
      if (this.map && TWEAK_CCS.includes(cc)) { st.cc[cc] = v; return [ccMsg(part, cc, this.effective(st, cc))]; }
      return [ccMsg(part, cc, v)];
    }
    program(part, v) {
      this.parts[part].pc = v;
      return this.select(part);
    }
    partMode(part, v) {
      this.parts[part].mode = v;
      return this.map ? this.select(part) : [];
    }
    select(part) {
      const st = this.parts[part];
      if (!this.map) return [pcMsg(part, st.pc)];     // the bank selects went through as they came
      const r = this.resolve(part, st);
      st.tweaks = r.tweaks;
      const out = [ccMsg(part, 0, r.msb), ccMsg(part, 32, r.lsb), pcMsg(part, r.program)];
      for (const n of TWEAK_CCS) out.push(ccMsg(part, n, this.effective(st, n)));
      return out;
    }
    resolve(part, st) {
      if (this.isDrum(part, st)) {
        const k = this.map.drums && this.map.drums[(st.msb === 126 ? 126 : 127) + ':' + st.pc];
        return { msb: 127, lsb: 0, program: k ? k.p : st.pc, tweaks: k && k.t || {} };
      }
      const e = this.map.voices && this.map.voices[st.msb + ':' + st.lsb + ':' + st.pc];
      if (e) return { msb: 0, lsb: e.b === undefined ? 0 : e.b, program: e.p === undefined ? st.pc : e.p, tweaks: e.t || {} };
      return { msb: 0, lsb: 0, program: st.pc, tweaks: {} };    // not in the map: the base voice
    }
  }

  // ---------------------------------------------------------------- stream from the emulator
  // Polls /tg, feeds the parser, and starts over when the unit was power cycled. The engine is
  // the built in synthesizer or a soundfont (sfengine.js, loaded on demand). While sound is off
  // the parser still follows the stream, so a newly started engine gets the current state.
  class QYSound {
    constructor() {
      this.builtin = new WebAudioEngine();
      this.sf = null;
      this.parser = new StreamParser(NULL_ENGINE);
      this.choice = 'builtin';      // 'builtin' or a file name in soundfonts/
      this.on = false;
      this.volume = 0.7;
      this.epoch = null; this.next = 0; this.busy = false; this.timer = null;
    }
    start() { if (!this.timer) this.timer = setInterval(() => this.poll(), 30); }
    async poll() {
      if (this.busy) return;
      this.busy = true;
      try {
        const r = await fetch('/tg?epoch=' + (this.epoch === null ? '' : this.epoch) + '&from=' + this.next, { cache: 'no-store' });
        const epoch = r.headers.get('X-QY-Epoch');
        if (epoch !== this.epoch) {          // a power cycle: the stream and its state start over
          this.parser.reset(); this.parser.clearState();
          const e = this.parser.eng; if (e !== NULL_ENGINE) e.reset();
          this.epoch = epoch;
        }
        this.next = Number(r.headers.get('X-QY-Next'));
        const bytes = new Uint8Array(await r.arrayBuffer());
        if (bytes.length) this.parser.feed(bytes);
      } catch (e) { /* server away, try again */ }
      this.busy = false;
    }
    setVolume(v) {
      this.volume = v;
      this.builtin.setVolume(v);
      if (this.sf) this.sf.setVolume(v);
    }
    async soundfontEngine() {
      if (!this.sf) {
        const mod = await import('/sfengine.js');
        this.sf = new mod.SoundfontEngine();
        this.sf.setVolume(this.volume);
      }
      return this.sf;
    }
    // Starts the chosen engine and moves the parser to it. Needs a user gesture the first time.
    async activate() {
      const old = this.parser.eng;
      let eng;
      if (this.choice === 'builtin') {
        eng = this.builtin;
        await eng.enable();
      } else {
        eng = await this.soundfontEngine();
        await eng.enable();
        await eng.loadFont(this.choice);
      }
      if (old !== NULL_ENGINE && old !== eng && old.disable) old.disable();
      this.parser.setEngine(eng);
    }
    async setEnabled(on) {
      this.on = on;
      if (on) { await this.activate(); return; }
      const old = this.parser.eng;
      this.parser.setEngine(NULL_ENGINE);
      if (old !== NULL_ENGINE && old.disable) old.disable();
    }
    // 'builtin' or a soundfont file name; takes effect at once when sound is on
    async choose(name) {
      this.choice = name;
      if (this.on) await this.activate();
    }
  }

  const api = { StreamParser, WebAudioEngine, QYSound, VoiceMapper, FAMILIES, DRUM_KIND, NULL_ENGINE };
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
  else window.QYSynth = api;
})();
