import { describe, expect, it } from 'vitest';
import type { CalendarEvent, Plan, Settings, Task } from './api';
import { due, nudges, type Nudge } from './nudges';

const date = '2026-10-06';
const at = (hhmm: string, day = date) => new Date(`${day}T${hhmm}:00`);
const iso = (hhmm: string, day = date) => at(hhmm, day).toISOString();
const eod = at('17:30');

const settings = (over: Partial<Settings> = {}): Settings => ({
	provider: 'openai',
	anthropic: { main_model: '', light_model: '', base_url: '' },
	openai: { main_model: '', light_model: '', base_url: '' },
	claudecode: { main_model: 'sonnet', light_model: 'haiku', base_url: '' },
	copilot: { main_model: 'claude-sonnet-4.5', light_model: 'gpt-5-mini', base_url: '' },
	style: '',
	about_me: '',
	end_of_day: '17:30',
	nudges: { before_event: true, event_end: true, late_day: true },
	alert_lead_minutes: 10,
	theme: '',
	onboarded: true,
	allowed_hosts: [],
	...over
});

const ev = (id: number, title: string, start: string, end: string | null, created = '08:00'): CalendarEvent => ({
	id,
	title,
	starts_at: iso(start),
	ends_at: end ? iso(end) : null,
	created_at: iso(created)
});

const task = (id: number, over: Partial<Task> = {}): Task => ({
	id,
	title: `task ${id}`,
	notes: '',
	raw_text: '',
	size: null,
	energy: null,
	feeling: '',
	due_on: date,
	not_before: null,
	status: 'open',
	refs: null,
	created_at: iso('09:00', '2026-10-05'),
	updated_at: iso('09:00', '2026-10-05'),
	...over
});

const plan = (musts: number[]): Plan => ({
	id: 1,
	opening: '',
	fallback: false,
	entries: musts.map((task_id) => ({ task_id, tier: 'must', rationale: '' }))
});

const times = (list: Nudge[]) => list.map((n) => [n.kind, new Date(n.at).toTimeString().slice(0, 5)]);

describe('nudges', () => {
	it('Should nudge before an event and when it ends', () => {
		const list = nudges([ev(1, 'Standup', '09:30', '09:45')], null, [], settings(), date, eod);
		expect(times(list)).toEqual([
			['before_event', '09:20'],
			['event_end', '09:45']
		]);
		expect(list.map((n) => n.text)).toEqual([
			'Standup in 10 min, find a stopping point.',
			"Standup's done. What now?"
		]);
	});

	it('Should use the lead time from settings', () => {
		const list = nudges([ev(1, 'Standup', '09:30', null)], null, [], settings({ alert_lead_minutes: 25 }), date, eod);
		expect(times(list)).toEqual([['before_event', '09:05']]);
		expect(list[0].text).toBe('Standup in 25 min, find a stopping point.');
	});

	it('Should skip the before nudge for an event added inside its own lead time', () => {
		const list = nudges([ev(1, 'Call', '14:00', '14:30', '13:55')], null, [], settings(), date, eod);
		expect(times(list)).toEqual([['event_end', '14:30']]);
	});

	it('Should give an event with no end no end nudge', () => {
		const list = nudges([ev(1, 'Focus', '10:00', null)], null, [], settings(), date, eod);
		expect(list.map((n) => n.kind)).toEqual(['before_event']);
	});

	it('Should nudge two hours before the end of day about a must due today that nothing touched', () => {
		const list = nudges([], plan([1]), [task(1, { title: 'File taxes' })], settings(), date, eod);
		expect(times(list)).toEqual([['late_day', '15:30']]);
		expect(list[0].text).toBe("File taxes is due today and hasn't moved yet. Still time.");
		expect(list[0].key).toBe(`late_day:${date}`);
	});

	it.each([
		['the must was touched today', [task(1, { updated_at: iso('08:00') })], [1]],
		['the task is a should, not a must', [task(1)], []],
		['the must is due tomorrow', [task(1, { due_on: '2026-10-07' })], [1]],
		['the must is done', [task(1, { status: 'done' })], [1]]
	])('Should skip the late-day nudge when %s', (_, tasks, musts) => {
		expect(nudges([], plan(musts), tasks, settings(), date, eod)).toEqual([]);
	});

	it('Should leave out the kinds turned off in settings', () => {
		const off = settings({ nudges: { before_event: false, event_end: true, late_day: false } });
		const list = nudges([ev(1, 'Standup', '09:30', '09:45')], plan([1]), [task(1)], off, date, eod);
		expect(list.map((n) => n.kind)).toEqual(['event_end']);
	});

	it('Should key a moved event apart from where it was', () => {
		const before = nudges([ev(1, 'Standup', '09:30', '09:45')], null, [], settings(), date, eod);
		const after = nudges([ev(1, 'Standup', '10:00', '10:15')], null, [], settings(), date, eod);
		expect(before[0].key).not.toBe(after[0].key);
	});
});

describe('due', () => {
	const list = nudges([ev(1, 'Standup', '09:30', '09:45')], null, [], settings(), date, eod);
	const t = (hhmm: string) => at(hhmm).getTime();

	it('Should fire a nudge in the tick that passes it, and not again in the next', () => {
		expect(due(list, t('09:19'), t('09:20')).map((n) => n.kind)).toEqual(['before_event']);
		expect(due(list, t('09:20'), t('09:21'))).toEqual([]);
	});

	it('Should not fire nudges that came before the page loaded', () => {
		expect(due(list, t('09:50'), t('09:51'))).toEqual([]);
	});

	it('Should fire every nudge inside a long gap between throttled ticks', () => {
		expect(due(list, t('09:00'), t('10:00')).map((n) => n.kind)).toEqual(['before_event', 'event_end']);
	});
});
