<script lang="ts">
	import type { KeySource, Provider } from '#lib/api.ts';

	let { provider, source }: { provider: Provider; source: KeySource } = $props();
</script>

{#if provider === 'claudecode'}
	<p>
		Uses the Claude Code you're signed in to, so your Claude plan's limits apply. For your own use on your own
		machine.
	</p>
	{#if source === 'login'}
		<p class="ok">Found <code>claude</code>.</p>
	{:else}
		<p class="bad">
			Install Claude Code first: <a href="https://code.claude.com" target="_blank" rel="noreferrer">https://code.claude.com</a>
		</p>
	{/if}
{:else}
	<p>Uses your GitHub Copilot plan. Each message counts toward your Copilot usage.</p>
	{#if source === 'login'}
		<p class="ok">Found the Copilot CLI.</p>
	{:else}
		<p class="bad">Install the Copilot CLI first: <code>brew install copilot-cli</code></p>
	{/if}
{/if}

<style>
	.ok {
		font-weight: var(--heavy);
	}
	.bad {
		color: var(--danger);
		font-weight: var(--heavy);
	}
</style>
