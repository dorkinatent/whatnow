<script lang="ts">
	import { onMount } from 'svelte';
	import { api, signsIn, type ConnectionResult, type Provider, type SettingsView } from '#lib/api.ts';
	import AboutYou from '#lib/components/AboutYou.svelte';
	import DangerZone from '#lib/components/DangerZone.svelte';
	import SignInNote from '#lib/components/SignInNote.svelte';
	import NotifyPermission from '#lib/components/NotifyPermission.svelte';
	import ThemePicker from '#lib/components/ThemePicker.svelte';
	import { live } from '#lib/live.svelte.ts';
	import { toasts } from '#lib/toasts.svelte.ts';

	let view = $state<SettingsView | null>(null);
	let key = $state('');
	let saving = $state(false);
	let testing = $state(false);
	let result = $state<ConnectionResult | null>(null);

	// Before any provider is saved, the form starts on the default one.
	onMount(() => {
		api.settings().then(
			(v) => {
				v.settings.provider ||= 'anthropic';
				view = v;
			},
			(err) => toasts.error(err)
		);
	});

	const provider = $derived((view?.settings.provider || 'anthropic') as Provider);
	const keyNote = $derived(
		view?.keys[provider] === 'env'
			? 'from env'
			: view?.keys[provider] === 'keychain'
				? 'saved'
				: 'not set'
	);

	// PUT takes the whole settings object, so this sends back everything GET returned. The key
	// is saved first, so whatever happens next, the key note is refetched to match.
	async function save(e: SubmitEvent) {
		e.preventDefault();
		if (!view) return;
		saving = true;
		result = null;
		view.settings.allowed_hosts = view.settings.allowed_hosts.map((h) => h.trim()).filter(Boolean);
		try {
			if (!signsIn(provider) && key.trim()) {
				await api.saveKey(provider, key.trim());
				key = '';
			}
			view = await api.saveSettings(view.settings);
			// A copy, so edits made after this save don't reach the rest of the app unsaved.
			live.view = $state.snapshot(view);
		} catch (err) {
			toasts.error(err);
			const fresh = await api.settings().catch(() => null);
			if (fresh && view) view.keys = fresh.keys;
		} finally {
			saving = false;
		}
	}

	async function test() {
		testing = true;
		try {
			result = await api.testConnection();
		} catch (err) {
			toasts.error(err);
		} finally {
			testing = false;
		}
	}
</script>

{#if view}
		<form class="sticker" onsubmit={save}>
			<h2 class="label">Connection</h2>
			<label>
				<span class="label">Provider</span>
				<select class="field" bind:value={view.settings.provider}>
					<option value="anthropic">Anthropic</option>
					<option value="openai">OpenAI-compatible</option>
					<option value="claudecode">Claude Code (your Claude plan)</option>
					<option value="copilot">GitHub Copilot</option>
				</select>
			</label>
			{#if signsIn(provider)}
				<SignInNote {provider} source={view.keys[provider]} />
			{:else}
				<label>
					<span class="label">API key <span class="muted">({keyNote})</span></span>
					<input class="field" type="password" bind:value={key} autocomplete="off" placeholder="Leave blank to keep" />
				</label>
			{/if}
			<label>
				<span class="label">Main model</span>
				<input class="field" bind:value={view.settings[provider].main_model} />
			</label>
			<label>
				<span class="label">Light model</span>
				<input class="field" bind:value={view.settings[provider].light_model} />
			</label>
			{#if provider === 'openai'}
				<label>
					<span class="label">Base URL</span>
					<input class="field" bind:value={view.settings.openai.base_url} placeholder="https://api.openai.com/v1" />
				</label>
			{/if}
			<div class="actions">
				<button type="button" class="btn" disabled={testing} onclick={test}>
					{testing ? 'Testing…' : 'Test connection'}
				</button>
				{#if result}
					<span class:ok={result.ok} class:bad={!result.ok}>
						{result.ok ? 'Connected' : result.error}
					</span>
				{/if}
			</div>

			<h2 class="label">You</h2>
			<AboutYou bind:style={view.settings.style} bind:aboutMe={view.settings.about_me} />

			<h2 class="label">Day</h2>
			<div class="row">
				<label>
					<span class="label">End of day</span>
					<input class="field" type="time" required bind:value={view.settings.end_of_day} />
				</label>
				<label>
					<span class="label">Event alerts, minutes before</span>
					<input class="field" type="number" min="1" max="60" required bind:value={view.settings.alert_lead_minutes} />
				</label>
			</div>

			<h2 class="label">Nudges</h2>
			<label class="check">
				<input type="checkbox" bind:checked={view.settings.nudges.before_event} />
				Before each event
			</label>
			<label class="check">
				<input type="checkbox" bind:checked={view.settings.nudges.event_end} />
				When an event ends, with a What now?
			</label>
			<label class="check">
				<input type="checkbox" bind:checked={view.settings.nudges.late_day} />
				Late in the day, if a must due today hasn't moved
			</label>
			<div><NotifyPermission /></div>

			<h2 class="label">Remote access</h2>
			<label>
				<span class="label">Allowed hosts, one per line</span>
				<textarea
					class="field"
					rows="2"
					placeholder="my-mac.tail1234.ts.net"
					bind:value={
						() => view!.settings.allowed_hosts.join('\n'),
						(v) => (view!.settings.allowed_hosts = v.split('\n'))
					}
				></textarea>
				<span class="muted">
					Hostnames besides this machine that can open whatnow, such as its Tailscale name behind
					<code>tailscale serve</code>. Anyone who can reach one gets full access.
				</span>
			</label>

			<div class="actions">
				<button class="btn primary" disabled={saving}>{saving ? 'Saving…' : 'Save'}</button>
			</div>
		</form>
		<ThemePicker bind:theme={view.settings.theme} />
		<DangerZone />
	{:else}
		<p class="muted">Loading…</p>
	{/if}

<style>
	h2 {
		margin: 10px 0 0;
		font-size: 14px;
	}
	h2:first-child {
		margin-top: 0;
	}
	form {
		display: flex;
		flex-direction: column;
		gap: 14px;
		padding: 20px;
	}
	label {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}
	.row {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 12px;
	}
	.check {
		flex-direction: row;
		align-items: center;
		gap: 8px;
	}
	.actions {
		display: flex;
		align-items: center;
		gap: 10px;
		flex-wrap: wrap;
	}
	.ok {
		font-weight: var(--heavy);
	}
	.bad {
		color: var(--danger);
		font-weight: var(--heavy);
	}
</style>
