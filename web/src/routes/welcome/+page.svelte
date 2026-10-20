<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { api, signsIn, type ConnectionResult, type Integration, type Provider, type SettingsView } from '#lib/api.ts';
	import AboutYou from '#lib/components/AboutYou.svelte';
	import Dots from '#lib/components/Dots.svelte';
	import IntegrationCard from '#lib/components/IntegrationCard.svelte';
	import SignInNote from '#lib/components/SignInNote.svelte';
	import NotifyPermission from '#lib/components/NotifyPermission.svelte';
	import { live } from '#lib/live.svelte.ts';
	import { toasts } from '#lib/toasts.svelte.ts';

	const steps = ['provider', 'key', 'test', 'you', 'integrations', 'nudges', 'ready'] as const;
	let at = $state(0);
	const step = $derived(steps[at]);

	let view = $state<SettingsView | null>(null);
	let key = $state('');
	/** Set when the user wants to type a key even though one came from the environment. */
	let ownKey = $state(false);
	let busy = $state(false);
	let result = $state<ConnectionResult | null>(null);
	let integrations = $state<Integration[]>([]);

	onMount(() => {
		api.settings().then(
			(v) => {
				v.settings.provider ||= 'anthropic';
				view = v;
			},
			(err) => toasts.error(err)
		);
		api.integrations().then(
			(l) => (integrations = l),
			(err) => toasts.error(err)
		);
	});

	const provider = $derived((view?.settings.provider || 'anthropic') as Provider);
	const envVar = $derived(provider === 'anthropic' ? 'ANTHROPIC_API_KEY' : 'OPENAI_API_KEY');
	const fromEnv = $derived(view?.keys[provider] === 'env' && !ownKey);
	const modelsSet = $derived(
		!!view?.settings[provider].main_model.trim() && !!view?.settings[provider].light_model.trim()
	);
	const keyReady = $derived(
		signsIn(provider)
			? view?.keys[provider] === 'login'
			: fromEnv || key.trim() !== '' || view?.keys[provider] === 'keychain'
	);

	/** Saves the form's settings. Returns whether it worked. */
	async function save(): Promise<boolean> {
		if (!view) return false;
		try {
			view = await api.saveSettings(view.settings);
			live.view = $state.snapshot(view);
			return true;
		} catch (err) {
			toasts.error(err);
			return false;
		}
	}

	async function saveKey() {
		busy = true;
		try {
			if (!signsIn(provider) && !fromEnv && key.trim()) {
				await api.saveKey(provider, key.trim());
				key = '';
			}
			if (await save()) {
				at++;
				await test();
			}
		} catch (err) {
			toasts.error(err);
		} finally {
			busy = false;
		}
	}

	async function test() {
		busy = true;
		result = null;
		try {
			result = await api.testConnection();
		} catch (err) {
			toasts.error(err);
		} finally {
			busy = false;
		}
	}

	async function saveYou() {
		busy = true;
		if (await save()) at++;
		busy = false;
	}

	async function finish() {
		if (!view) return;
		view.settings.onboarded = true;
		if (await save()) at++;
	}

	// Skipping keeps only what was already saved, so a half-chosen provider never sticks.
	async function later() {
		try {
			const saved = await api.settings();
			saved.settings.onboarded = true;
			live.view = await api.saveSettings(saved.settings);
			goto('/', { replaceState: true });
		} catch (err) {
			toasts.error(err);
		}
	}
</script>

<main class="welcome">
	<Dots count={steps.length} {at} />
	{#if view}
		<section class="sticker card">
			{#if step === 'provider'}
				<h1>Hi, I'm whatnow</h1>
				<p>I turn the pile in your head into one next thing to do. First, which model should I use?</p>
				<div class="choices">
					<button
						class="sticker choice"
						class:on={provider === 'anthropic'}
						onclick={() => view && (view.settings.provider = 'anthropic')}
					>
						<strong>Anthropic</strong>
						<span class="muted">Claude</span>
					</button>
					<button
						class="sticker choice"
						class:on={provider === 'openai'}
						onclick={() => view && (view.settings.provider = 'openai')}
					>
						<strong>OpenAI-compatible</strong>
						<span class="muted">OpenAI, Ollama, OpenRouter, and others</span>
					</button>
					<button
						class="sticker choice"
						class:on={provider === 'claudecode'}
						onclick={() => view && (view.settings.provider = 'claudecode')}
					>
						<strong>Claude Code</strong>
						<span class="muted">Your Claude plan, through the claude you're signed in to</span>
					</button>
					<button
						class="sticker choice"
						class:on={provider === 'copilot'}
						onclick={() => view && (view.settings.provider = 'copilot')}
					>
						<strong>GitHub Copilot</strong>
						<span class="muted">Your Copilot plan, through the Copilot CLI</span>
					</button>
				</div>
				{#if provider === 'openai'}
					<label>
						<span class="label">Base URL <span class="muted">(optional)</span></span>
						<input class="field" bind:value={view.settings.openai.base_url} placeholder="https://api.openai.com/v1" />
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
			{:else if step === 'key'}
				{#if signsIn(provider)}
					<h1>Your sign-in</h1>
					<SignInNote {provider} source={view.keys[provider]} />
				{:else}
					<h1>Your API key</h1>
					{#if fromEnv}
						<p>Found your key in <code>{envVar}</code>.</p>
						<button class="link" onclick={() => (ownKey = true)}>Use a different key</button>
					{:else}
						<p>It stays in your system keychain, never in whatnow's database.</p>
						<input
							class="field"
							type="password"
							bind:value={key}
							autocomplete="off"
							aria-label="API key"
							placeholder={view.keys[provider] === 'keychain' ? 'Saved. Leave blank to keep' : ''}
						/>
					{/if}
				{/if}
			{:else if step === 'test'}
				<h1>Checking the connection</h1>
				{#if busy}
					<p class="muted">Saying hello…</p>
				{:else if result?.ok}
					<p class="ok">Connected.</p>
				{:else if result}
					<p class="bad">{result.error}</p>
					<button class="btn" onclick={test}>Try again</button>
				{/if}
			{:else if step === 'you'}
				<h1>About you</h1>
				<p>Optional. Both can change later in Settings.</p>
				<AboutYou bind:style={view.settings.style} bind:aboutMe={view.settings.about_me} />
			{:else if step === 'integrations'}
				<h1>Bring in your work</h1>
				<p>
					Optional. Pull today's meetings and your tickets into the morning. Both can change later in
					Settings → Integrations.
				</p>
				{#each integrations as integration, i (integration.id)}
					<IntegrationCard bind:integration={integrations[i]} />
				{/each}
				<p class="muted">Save each one you set up.</p>
			{:else if step === 'nudges'}
				<h1>Nudges</h1>
				<ul>
					<li>A heads-up before each event, so you can find a stopping point.</li>
					<li>A What now? when an event ends.</li>
					<li>One late-day check if a must due today hasn't moved.</li>
				</ul>
				<p class="muted">Without notifications, they show in the page while it's open.</p>
				<NotifyPermission />
			{:else}
				<h1>You're set</h1>
				<p>Let's plan your first day.</p>
			{/if}
		</section>

		<div class="nav">
			{#if at > 0 && step !== 'ready'}
				<button class="btn" disabled={busy} onclick={() => at--}>Back</button>
			{/if}
			<span class="spacer"></span>
			{#if step !== 'ready'}
				<button class="link" onclick={later}>Set up later</button>
			{/if}
			{#if step === 'provider'}
				<button class="btn primary" disabled={!modelsSet} onclick={() => at++}>Next</button>
			{:else if step === 'key'}
				<button class="btn primary" disabled={!keyReady || busy} onclick={saveKey}>
					{busy ? 'Saving…' : 'Next'}
				</button>
			{:else if step === 'test'}
				<button class="btn primary" disabled={!result?.ok} onclick={() => at++}>Next</button>
			{:else if step === 'you'}
				<button class="btn" onclick={() => at++}>Skip</button>
				<button class="btn primary" disabled={busy} onclick={saveYou}>Next</button>
			{:else if step === 'integrations'}
				<button class="btn" onclick={() => at++}>Skip</button>
				<button class="btn primary" onclick={() => at++}>Next</button>
			{:else if step === 'nudges'}
				<button class="btn primary" onclick={finish}>Next</button>
			{:else}
				<button class="btn primary" onclick={() => goto('/morning')}>Start my day</button>
			{/if}
		</div>
	{:else}
		<p class="muted">Loading…</p>
	{/if}
</main>

<style>
	.welcome {
		max-width: 560px;
		margin: 0 auto;
		padding: 32px 16px;
		display: flex;
		flex-direction: column;
		gap: 18px;
	}
	.card {
		padding: 24px;
		display: flex;
		flex-direction: column;
		gap: 14px;
	}
	h1 {
		margin: 0;
		font-weight: var(--heavy);
	}
	p {
		margin: 0;
	}
	label {
		display: flex;
		flex-direction: column;
		gap: 4px;
	}
	.choices {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 12px;
	}
	.choice {
		display: flex;
		flex-direction: column;
		gap: 4px;
		padding: 14px;
		text-align: left;
	}
	.choice.on {
		background: var(--chip);
	}
	.nav {
		display: flex;
		align-items: center;
		gap: 10px;
	}
	.spacer {
		flex: 1;
	}
	.link {
		background: none;
		border: none;
		padding: 0;
		color: var(--primary);
		font-weight: var(--heavy);
		text-align: left;
	}
	.ok {
		font-weight: var(--heavy);
	}
	.bad {
		color: var(--danger);
		font-weight: var(--heavy);
	}

	@media (max-width: 600px) {
		.choices {
			grid-template-columns: 1fr;
		}
	}
</style>
