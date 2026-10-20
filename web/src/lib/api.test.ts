import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError, api, configured, signsIn, type KeySource, type Provider, type SettingsView } from './api';

afterEach(() => vi.unstubAllGlobals());

function stubFetch(status: number, body: unknown) {
	const fetch = vi.fn(async () => new Response(status === 204 ? null : JSON.stringify(body), { status }));
	vi.stubGlobal('fetch', fetch);
	return fetch;
}

describe('api', () => {
	it('Should send every write as JSON, even with no body to send', async () => {
		const fetch = stubFetch(204, null);
		await api.undo(4);
		expect(fetch).toHaveBeenCalledWith('/api/v1/batches/4/undo', {
			method: 'POST',
			headers: { 'Content-Type': 'application/json' },
			body: '{}'
		});
	});

	it("Should throw the server's message with its status", async () => {
		stubFetch(409, { error: 'a reply is still being written' });
		await expect(api.send('hi')).rejects.toEqual(new ApiError(409, 'a reply is still being written'));
	});
});

describe('configured', () => {
	const view = (provider: '' | Provider, key: KeySource): SettingsView => ({
		settings: {
			provider,
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
			allowed_hosts: []
		},
		keys: { anthropic: '', openai: '', claudecode: '', copilot: '', ...(provider ? { [provider]: key } : {}) }
	});

	it.each([
		['no provider', false, view('', 'env')],
		['a provider with no key', false, view('anthropic', '')],
		['a provider with a key', true, view('anthropic', 'env')],
		['a sign-in provider whose CLI is found', true, view('claudecode', 'login')],
		['a sign-in provider whose CLI is missing', false, view('copilot', '')]
	])('Should count %s as set up: %s', (_, want, v) => {
		expect(configured(v)).toBe(want);
	});
});

describe('signsIn', () => {
	it.each([
		['anthropic', false],
		['openai', false],
		['claudecode', true],
		['copilot', true]
	] as const)('Should say whether %s signs in through its CLI', (p, want) => {
		expect(signsIn(p)).toBe(want);
	});
});
