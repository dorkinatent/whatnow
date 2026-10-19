import { describe, expect, it } from 'vitest';
import type { Settings } from './api';
import { welcomeDue } from './welcome';

const fresh: Settings = {
	provider: '',
	anthropic: { main_model: 'claude-sonnet-5-5', light_model: 'claude-haiku-4-5', base_url: '' },
	openai: { main_model: '', light_model: '', base_url: '' },
	claudecode: { main_model: 'sonnet', light_model: 'haiku', base_url: '' },
	copilot: { main_model: 'claude-sonnet-4.5', light_model: 'gpt-5-mini', base_url: '' },
	style: '',
	about_me: '',
	end_of_day: '17:30',
	nudges: { before_event: true, event_end: true, late_day: true },
	alert_lead_minutes: 10,
	theme: '',
	onboarded: false,
	allowed_hosts: []
};

describe('welcomeDue', () => {
	it.each([
		['a fresh install opening Today', fresh, '/', true],
		['a fresh install opening the morning', fresh, '/morning', true],
		['setup was skipped', { ...fresh, onboarded: true }, '/', false],
		['a provider is saved', { ...fresh, provider: 'anthropic' }, '/', false],
		['already in onboarding', fresh, '/welcome', false],
		['in Settings', fresh, '/settings', false],
		["in Settings' integrations", fresh, '/settings/integrations', false]
	] as const)('Should know whether to onboard when %s', (_, s, path, want) => {
		expect(welcomeDue(s, path)).toBe(want);
	});
});
