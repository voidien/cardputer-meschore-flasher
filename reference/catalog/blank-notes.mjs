// Replaces Ripple Radios' release notes with a placeholder in a saved catalog input, so their text, whose licence is
// unclear, stays out of the repository (the owner's choice). The placeholder keeps the line shape, so the notes still
// go through the same clean-up in the reference and in Go. Nothing else changes:
//
//   - a /releases?repo=github-ripple list (compact JSON, as the site sends it, which JSON.stringify reproduces): every
//     release's notes;
//   - a config.json (hand-formatted, so it is edited as text, not re-serialised): the notes of the inline versions of
//     every firmware entry whose class is "ripple". The result is checked to parse to the same catalog with only those
//     notes changed.
//
//   node blank-notes.mjs < saved-input.json > blanked.json

import { readFileSync } from 'node:fs';
import { deepStrictEqual } from 'node:assert';

const PLACEHOLDER = "Change log:\r\n* left out of meshflash's test data (the text's licence is unclear)";

const raw = readFileSync(0, 'utf8');
const data = JSON.parse(raw);

if (Array.isArray(data)) {
	for (const r of data) {
		if (typeof r.notes === 'string' && r.notes !== '') r.notes = PLACEHOLDER;
	}
	process.stdout.write(JSON.stringify(data));
} else if (data && Array.isArray(data.device)) {
	// The notes to replace, and the catalog as it must parse afterwards.
	const ripple = new Set();
	const other = new Set();
	const want = structuredClone(data);
	for (const d of want.device) {
		for (const fw of d.firmware ?? []) {
			const versions = fw && typeof fw.version === 'object' && fw.version ? Object.values(fw.version) : [];
			for (const v of versions) {
				if (!v || typeof v.notes !== 'string' || v.notes === '') continue;
				if (fw.class === 'ripple') {
					ripple.add(v.notes);
					v.notes = PLACEHOLDER;
				} else {
					other.add(v.notes);
				}
			}
		}
	}
	for (const n of ripple) {
		if (other.has(n)) throw new Error('a Ripple note is also used outside Ripple firmware; edit by hand');
	}
	const out = raw.replace(/("notes"\s*:\s*)("(?:[^"\\]|\\.)*")/g, (all, key, literal) =>
		ripple.has(JSON.parse(literal)) ? key + JSON.stringify(PLACEHOLDER) : all);
	deepStrictEqual(JSON.parse(out), want);
	process.stdout.write(out);
} else {
	throw new Error('expected a release list or a config.json');
}
